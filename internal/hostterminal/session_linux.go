//go:build linux

package hostterminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/iivankin/platformd/internal/terminaltransport"
	"golang.org/x/sys/unix"
)

const (
	hostShell          = "/bin/bash"
	cgroupCloseTimeout = 2 * time.Second
)

type processResult struct {
	exitCode int
	err      error
}

type session struct {
	pty     *os.File
	command *exec.Cmd
	leaf    Leaf
	done    chan struct{}
	finish  func(string, int, error) error

	resultMu  sync.Mutex
	result    processResult
	closeOnce sync.Once
	closeErr  error
}

func spawnPTY(ctx context.Context, config spawnConfig) (terminaltransport.Session, error) {
	leaf, err := config.createLeaf(config.leafID)
	if err != nil {
		return nil, fmt.Errorf("create host terminal cgroup: %w", err)
	}
	command := exec.Command(hostShell, "--login")
	command.Dir = "/root"
	command.Env = []string{
		"HOME=/root",
		"LANG=C.UTF-8",
		"LOGNAME=root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SHELL=/bin/bash",
		"TERM=xterm-256color",
		"USER=root",
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig:   syscall.SIGKILL,
		UseCgroupFD: true,
		CgroupFD:    int(leaf.FD()),
	}
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Cols: config.size.Cols, Rows: config.size.Rows})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start host terminal: %w", err), cleanupLeaf(leaf))
	}
	// creack/pty intentionally leaves the descriptor blocking. Registering it
	// with Go's poller makes Close reliably interrupt a concurrent Read when the
	// WebSocket disappears.
	if err := unix.SetNonblock(int(terminal.Fd()), true); err != nil {
		_ = leaf.Kill()
		_ = killProcessGroup(command)
		_ = terminal.Close()
		_ = command.Wait()
		return nil, errors.Join(fmt.Errorf("make host terminal nonblocking: %w", err), cleanupLeaf(leaf))
	}
	result := &session{
		pty: terminal, command: command, leaf: leaf, done: make(chan struct{}), finish: config.finish,
	}
	go func() {
		runErr := command.Wait()
		exitCode := -1
		if command.ProcessState != nil {
			exitCode = command.ProcessState.ExitCode()
		}
		result.resultMu.Lock()
		result.result = processResult{exitCode: exitCode, err: runErr}
		result.resultMu.Unlock()
		close(result.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = result.Close("context_cancelled")
		case <-result.done:
		}
	}()
	return result, nil
}

func (session *session) Read(target []byte) (int, error) {
	count, err := session.pty.Read(target)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return count, err
}

func (session *session) Write(payload []byte) (int, error) {
	return session.pty.Write(payload)
}

func (session *session) Resize(size terminaltransport.Size) error {
	return pty.Setsize(session.pty, &pty.Winsize{Cols: size.Cols, Rows: size.Rows})
}

func (session *session) Wait() (int, error) {
	<-session.done
	result := session.processResult()
	return result.exitCode, result.err
}

func (session *session) Close(reason string) error {
	session.closeOnce.Do(func() {
		process, exited := session.completed()
		killErr := errors.Join(session.leaf.Kill(), killProcessGroup(session.command))
		_ = session.pty.Close()
		if !exited {
			<-session.done
			process = session.processResult()
			process.err = errors.Join(context.Canceled, killErr)
		}
		cleanupErr := cleanupLeaf(session.leaf)
		finishRunErr := process.err
		if killErr != nil || cleanupErr != nil {
			finishRunErr = errors.Join(killErr, cleanupErr)
		}
		finishErr := session.finish(reason, process.exitCode, finishRunErr)
		session.closeErr = errors.Join(killErr, cleanupErr, finishErr)
	})
	return session.closeErr
}

func (session *session) completed() (processResult, bool) {
	select {
	case <-session.done:
		return session.processResult(), true
	default:
		return processResult{exitCode: -1}, false
	}
}

func (session *session) processResult() processResult {
	session.resultMu.Lock()
	defer session.resultMu.Unlock()
	return session.result
}

func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func cleanupLeaf(leaf Leaf) error {
	ctx, cancel := context.WithTimeout(context.Background(), cgroupCloseTimeout)
	defer cancel()
	return leaf.Close(ctx)
}
