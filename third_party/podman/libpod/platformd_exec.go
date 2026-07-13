//go:build !remote

package libpod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containers/podman/v5/libpod/define"
	"go.podman.io/common/pkg/resize"
)

// ExecContext is the cancellable, attached exec primitive required by an
// embedding daemon. Upstream Exec has no context; platformd must be able to
// stop the exact exec session when an API request is cancelled or times out.
func (c *Container) ExecContext(ctx context.Context, config *ExecConfig, streams *define.AttachStreams) (exitCode int, retErr error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	sessionID, err := c.ExecCreate(config)
	if err != nil {
		return -1, err
	}
	defer func() {
		if err := c.ExecRemove(sessionID, true); err != nil && retErr == nil && !errors.Is(err, define.ErrNoSuchExecSession) {
			exitCode = -1
			retErr = err
		}
	}()

	attachDone := make(chan error, 1)
	go func() {
		attachDone <- c.execStartAndAttach(sessionID, streams, nil, false)
	}()

	select {
	case err := <-attachDone:
		if err != nil {
			return -1, err
		}
	case <-ctx.Done():
		if err := c.stopExecAfterStart(sessionID); err != nil {
			return -1, fmt.Errorf("stop cancelled exec session: %w", err)
		}
		if err := <-attachDone; err != nil && !errors.Is(err, define.ErrExecSessionStateInvalid) {
			return -1, fmt.Errorf("wait for cancelled exec session: %w", err)
		}
		return -1, ctx.Err()
	}

	session, err := c.execSessionNoCopy(sessionID)
	if err != nil {
		if errors.Is(err, define.ErrNoSuchExecSession) {
			diedEvent, eventErr := c.runtime.GetExecDiedEvent(context.Background(), c.ID(), sessionID)
			if eventErr != nil {
				return -1, fmt.Errorf("retrieve exec session exit code: %w", eventErr)
			}
			return *diedEvent.ContainerExitCode, nil
		}
		return -1, err
	}
	return session.ExitCode, nil
}

// ExecTerminalContext is the cancellable TTY variant used by platformd's
// interactive console. It owns the resize worker and stops only the exact exec
// session when the browser disconnects, unlike stopping the workload container.
func (c *Container) ExecTerminalContext(ctx context.Context, config *ExecConfig, streams *define.AttachStreams, initial resize.TerminalSize, resizes <-chan resize.TerminalSize) (exitCode int, retErr error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if config == nil || !config.Terminal {
		return -1, fmt.Errorf("terminal exec requires a TTY config: %w", define.ErrInvalidArg)
	}
	if initial.Width < 1 || initial.Height < 1 {
		return -1, fmt.Errorf("terminal exec requires a positive initial size: %w", define.ErrInvalidArg)
	}
	sessionID, err := c.ExecCreate(config)
	if err != nil {
		return -1, err
	}
	defer func() {
		if err := c.ExecRemove(sessionID, true); err != nil && retErr == nil && !errors.Is(err, define.ErrNoSuchExecSession) {
			exitCode = -1
			retErr = err
		}
	}()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resizeDone := make(chan struct{})
	go func() {
		defer close(resizeDone)
		c.forwardExecResizes(runCtx, sessionID, resizes)
	}()
	defer func() { cancel(); <-resizeDone }()

	attachDone := make(chan error, 1)
	go func() {
		attachDone <- c.execStartAndAttach(sessionID, streams, &initial, false)
	}()

	select {
	case err := <-attachDone:
		if err != nil {
			return -1, err
		}
	case <-ctx.Done():
		if err := c.stopExecAfterStart(sessionID); err != nil {
			return -1, fmt.Errorf("stop cancelled terminal exec session: %w", err)
		}
		if err := <-attachDone; err != nil && !errors.Is(err, define.ErrExecSessionStateInvalid) {
			return -1, fmt.Errorf("wait for cancelled terminal exec session: %w", err)
		}
		return -1, ctx.Err()
	}

	session, err := c.execSessionNoCopy(sessionID)
	if err != nil {
		if errors.Is(err, define.ErrNoSuchExecSession) {
			diedEvent, eventErr := c.runtime.GetExecDiedEvent(context.Background(), c.ID(), sessionID)
			if eventErr != nil {
				return -1, fmt.Errorf("retrieve terminal exec session exit code: %w", eventErr)
			}
			return *diedEvent.ContainerExitCode, nil
		}
		return -1, err
	}
	return session.ExitCode, nil
}

func (c *Container) forwardExecResizes(ctx context.Context, sessionID string, resizes <-chan resize.TerminalSize) {
	if resizes == nil {
		<-ctx.Done()
		return
	}
	// execStartAndAttach installs the initial size before starting the process.
	// Wait until the session is running so an early browser ResizeObserver event
	// cannot make the resize worker exit on the transient Created state.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			session, err := c.ExecSession(sessionID)
			if err != nil {
				return
			}
			switch session.State {
			case define.ExecStateRunning:
				goto running
			case define.ExecStateStopped:
				return
			}
		}
	}

running:
	for {
		select {
		case <-ctx.Done():
			return
		case size, ok := <-resizes:
			if !ok {
				return
			}
			if size.Width < 1 || size.Height < 1 {
				continue
			}
			if err := c.ExecResize(sessionID, size); err != nil {
				return
			}
		}
	}
}

func (c *Container) stopExecAfterStart(sessionID string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		session, err := c.ExecSession(sessionID)
		if err != nil {
			if errors.Is(err, define.ErrNoSuchExecSession) {
				return nil
			}
			return err
		}
		switch session.State {
		case define.ExecStateRunning:
			zero := uint(0)
			return c.ExecStop(sessionID, &zero)
		case define.ExecStateStopped:
			return nil
		case define.ExecStateCreated:
			if time.Now().After(deadline) {
				return fmt.Errorf("exec session did not leave created state")
			}
			time.Sleep(5 * time.Millisecond)
		default:
			return fmt.Errorf("unexpected exec session state %s", session.State.String())
		}
	}
}
