package rootexec

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/id"
)

const processWaitDelay = 2 * time.Second
const cgroupCleanupTimeout = 2 * time.Second

var ErrInvalidRequest = errors.New("invalid server exec request")

type Runner struct {
	createLeaf     func(string) (Leaf, error)
	random         io.Reader
	now            func() time.Time
	commandBytes   int
	outputBytes    int
	defaultTimeout time.Duration
	maximumTimeout time.Duration
	semaphore      chan struct{}
}

func New(config Config) (*Runner, error) {
	if config.CreateLeaf == nil || config.CommandBytes < 1 || config.OutputBytes < 1 || config.DefaultTimeout <= 0 ||
		config.MaximumTimeout < config.DefaultTimeout || config.MaximumParallel < 1 {
		return nil, errors.New("server exec dependencies and limits are incomplete")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Runner{
		createLeaf: config.CreateLeaf, random: config.Random, now: config.Now,
		commandBytes: config.CommandBytes, outputBytes: config.OutputBytes,
		defaultTimeout: config.DefaultTimeout, maximumTimeout: config.MaximumTimeout,
		semaphore: make(chan struct{}, config.MaximumParallel),
	}, nil
}

func (runner *Runner) Execute(ctx context.Context, request Request) (Result, error) {
	if request.Command == "" || len(request.Command) > runner.commandBytes || !utf8.ValidString(request.Command) || strings.ContainsRune(request.Command, '\x00') {
		return Result{}, fmt.Errorf("%w: command must be valid non-empty UTF-8 without NUL and at most %d bytes", ErrInvalidRequest, runner.commandBytes)
	}
	timeout := request.Timeout
	if timeout == 0 {
		timeout = runner.defaultTimeout
	}
	if timeout <= 0 || timeout > runner.maximumTimeout {
		return Result{}, fmt.Errorf("%w: timeout must be at most %s", ErrInvalidRequest, runner.maximumTimeout)
	}
	select {
	case runner.semaphore <- struct{}{}:
		defer func() { <-runner.semaphore }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	started := runner.now()
	executionID, err := id.NewWith(started, runner.random)
	if err != nil {
		return Result{}, fmt.Errorf("allocate server exec ID: %w", err)
	}
	leaf, err := runner.createLeaf("exec-" + executionID)
	if err != nil {
		return Result{}, err
	}
	result, runErr := runner.run(ctx, leaf, request.Command, timeout, started)
	cleanupErr := leaf.Kill()
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), cgroupCleanupTimeout)
	closeErr := leaf.Close(cleanupCtx)
	cancelCleanup()
	return result, errors.Join(runErr, cleanupErr, closeErr)
}

func (runner *Runner) run(ctx context.Context, leaf Leaf, commandText string, timeout time.Duration, started time.Time) (Result, error) {
	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout := newBoundedBuffer(runner.outputBytes)
	stderr := newBoundedBuffer(runner.outputBytes)
	command := exec.CommandContext(executionContext, "/bin/sh", "-lc", commandText)
	attributes, err := processAttributes(leaf.FD())
	if err != nil {
		return Result{}, err
	}
	command.SysProcAttr = attributes
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		groupErr := leaf.Kill()
		processErr := command.Process.Kill()
		if errors.Is(processErr, os.ErrProcessDone) && groupErr == nil {
			return os.ErrProcessDone
		}
		return errors.Join(groupErr, processErr)
	}
	runErr := command.Run()
	finished := runner.now()
	stdoutValue, stdoutTruncated := stdout.Result()
	stderrValue, stderrTruncated := stderr.Result()
	result := Result{
		Stdout: strings.ToValidUTF8(stdoutValue, "�"), Stderr: strings.ToValidUTF8(stderrValue, "�"),
		ExitCode: -1, StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated,
		DurationMillis: max(0, finished.Sub(started).Milliseconds()),
		StartedAt:      started.UnixMilli(), FinishedAt: finished.UnixMilli(),
		TimedOut:  errors.Is(executionContext.Err(), context.DeadlineExceeded),
		Cancelled: errors.Is(executionContext.Err(), context.Canceled) && !errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	var exitError *exec.ExitError
	if runErr == nil || errors.As(runErr, &exitError) || result.TimedOut || result.Cancelled {
		return result, nil
	}
	return result, fmt.Errorf("execute host command: %w", runErr)
}
