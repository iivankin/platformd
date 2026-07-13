//go:build linux && integration

package hostterminal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

func TestHostPTYResizesAndKillsItsProcessTree(t *testing.T) {
	if os.Getenv("PLATFORMD_HOST_TERMINAL_INTEGRATION") != "1" {
		t.Skip("run inside a delegated systemd unit with PLATFORMD_HOST_TERMINAL_INTEGRATION=1")
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	freezeContext, cancelFreeze := context.WithTimeout(context.Background(), time.Second)
	if err := tree.SetFrozen(freezeContext, true); err != nil {
		cancelFreeze()
		t.Fatal(err)
	}
	cancelFreeze()
	defer func() {
		unfreezeContext, cancelUnfreeze := context.WithTimeout(context.Background(), time.Second)
		defer cancelUnfreeze()
		if err := tree.SetFrozen(unfreezeContext, false); err != nil {
			t.Errorf("unfreeze workload subtree: %v", err)
		}
	}()
	finished := make(chan error, 1)
	session, err := spawnPTY(context.Background(), spawnConfig{
		createLeaf: func(identifier string) (Leaf, error) { return tree.CreateOperationLeaf(identifier) },
		leafID:     "terminal-integration", size: terminaltransport.Size{Cols: 80, Rows: 24},
		finish: func(reason string, _ int, runErr error) error {
			if reason != "client_closed" || !errors.Is(runErr, context.Canceled) {
				err := errors.New("unexpected terminal completion")
				finished <- err
				return err
			}
			finished <- nil
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Write([]byte("stty -echo; printf '__SIZE1__'; stty size\n")); err != nil {
		t.Fatal(err)
	}
	output := readPTYUntil(t, session, []byte("__SIZE1__24 80"))
	if !bytes.Contains(output, []byte("__SIZE1__24 80")) {
		t.Fatalf("initial terminal output = %q", output)
	}
	if err := session.Resize(terminaltransport.Size{Cols: 100, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Write([]byte("printf '__SIZE2__'; stty size; sleep 300 & printf '__CHILD__%s__\\n__BLOCKING__' \"$!\"; wait\n")); err != nil {
		t.Fatal(err)
	}
	output = readPTYUntil(t, session, []byte("__BLOCKING__"))
	if !bytes.Contains(output, []byte("__SIZE2__40 100")) {
		t.Fatalf("resized terminal output = %q", output)
	}
	match := regexp.MustCompile(`__CHILD__(\d+)__`).FindSubmatch(output)
	if len(match) != 2 {
		output = append(output, readPTYUntil(t, session, []byte("__"))...)
		match = regexp.MustCompile(`__CHILD__(\d+)__`).FindSubmatch(output)
	}
	if len(match) != 2 {
		t.Fatalf("child PID output = %q", output)
	}
	childPID, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1)
		_, readErr := session.Read(buffer)
		readDone <- readErr
	}()
	time.Sleep(50 * time.Millisecond)
	if err := session.Close("client_closed"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("closing the PTY returned a successful blocked read")
		}
	case <-time.After(time.Second):
		t.Fatal("closing the PTY did not interrupt a blocked read")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal completion was not audited")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal child PID %d survived close: %v", childPID, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readPTYUntil(t *testing.T, session terminaltransport.Session, marker []byte) []byte {
	t.Helper()
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var output bytes.Buffer
		buffer := make([]byte, 4096)
		for {
			count, err := session.Read(buffer)
			if count > 0 {
				_, _ = output.Write(buffer[:count])
				if bytes.Contains(output.Bytes(), marker) {
					done <- result{output: output.Bytes()}
					return
				}
			}
			if err != nil {
				done <- result{output: output.Bytes(), err: err}
				return
			}
		}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			t.Fatalf("read host PTY: %v (%q)", value.err, value.output)
		}
		return value.output
	case <-time.After(3 * time.Second):
		t.Fatal("timed out reading host PTY")
		return nil
	}
}
