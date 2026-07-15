package daemon

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

func (stack *runtimeStack) ResourceContainer(kind, resourceID string) (containerengine.Container, bool, error) {
	stack.mu.Lock()
	deployments := stack.deployments
	postgres := stack.managedPostgres
	redis := stack.managedRedis
	closed := stack.closed
	stack.mu.Unlock()
	if closed {
		return containerengine.Container{}, false, errors.New("container runtime is closed")
	}
	var container containerengine.Container
	var active bool
	var err error
	switch kind {
	case "service":
		if deployments == nil {
			return containerengine.Container{}, false, errors.New("service runtime is not configured")
		}
		container, active, err = deployments.Container(resourceID)
	case "postgres":
		if postgres == nil {
			return containerengine.Container{}, false, errors.New("PostgreSQL runtime is not configured")
		}
		container, active, err = postgres.Status(resourceID)
	case "redis":
		if redis == nil {
			return containerengine.Container{}, false, errors.New("Redis runtime is not configured")
		}
		container, active, err = redis.Status(resourceID)
	default:
		return containerengine.Container{}, false, errors.New("resource kind has no container")
	}
	if err != nil || !active || container.State != "running" {
		return containerengine.Container{}, false, err
	}
	return container, true, nil
}

func (stack *runtimeStack) ExecResourceTerminal(ctx context.Context, kind, resourceID, expectedContainerID string, request containerengine.TerminalExecRequest) (int, error) {
	container, active, err := stack.ResourceContainer(kind, resourceID)
	if err != nil {
		return -1, err
	}
	if !active || container.ID != expectedContainerID {
		return -1, errors.New("resource terminal target is no longer active")
	}
	return stack.engine.ExecTerminalContainer(ctx, expectedContainerID, request)
}

func (stack *runtimeStack) ProbeResourceTerminalShell(ctx context.Context, kind, resourceID, expectedContainerID, shell string) bool {
	container, active, err := stack.ResourceContainer(kind, resourceID)
	if err != nil || !active || container.ID != expectedContainerID {
		return false
	}
	exitCode, err := stack.engine.ExecContainer(ctx, expectedContainerID, containerengine.ExecRequest{
		Command: []string{shell, "-c", "exit 0"},
	})
	return err == nil && exitCode == 0
}

type liveContainerResourceRepository struct{ store *state.Store }

func (repository liveContainerResourceRepository) Resource(ctx context.Context, projectID, kind, resourceID string) error {
	switch kind {
	case "service":
		_, err := repository.store.Service(ctx, projectID, resourceID)
		return err
	case "postgres":
		_, err := repository.store.ManagedPostgresInProject(ctx, projectID, resourceID)
		return err
	case "redis":
		_, err := repository.store.ManagedRedisInProject(ctx, projectID, resourceID)
		return err
	default:
		return errors.New("resource kind has no container")
	}
}
