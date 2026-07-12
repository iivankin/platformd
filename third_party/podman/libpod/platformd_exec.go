//go:build !remote

package libpod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containers/podman/v5/libpod/define"
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
