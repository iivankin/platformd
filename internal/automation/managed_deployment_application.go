package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
)

type ManagedDeploymentRestarter interface {
	RestartDeployment(context.Context, string, string, string) error
	RemoveDeployment(context.Context, string, string, string) error
}

type ManagedDeploymentApplication struct {
	redis    ManagedDeploymentRestarter
	postgres ManagedDeploymentRestarter
}

type ManagedDeploymentActionInput struct {
	ProjectID    string
	Kind         string
	ResourceID   string
	DeploymentID string
}

func NewManagedDeploymentApplication(redis, postgres ManagedDeploymentRestarter) (*ManagedDeploymentApplication, error) {
	if redis == nil || postgres == nil {
		return nil, errors.New("managed deployment automation dependencies are incomplete")
	}
	return &ManagedDeploymentApplication{redis: redis, postgres: postgres}, nil
}

func (application *ManagedDeploymentApplication) Restart(ctx context.Context, identity Identity, input ManagedDeploymentActionInput) error {
	return application.action(ctx, identity, input, true)
}

func (application *ManagedDeploymentApplication) Remove(ctx context.Context, identity Identity, input ManagedDeploymentActionInput) error {
	return application.action(ctx, identity, input, false)
}

func (application *ManagedDeploymentApplication) action(ctx context.Context, identity Identity, input ManagedDeploymentActionInput, restart bool) error {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return err
	}
	if input.ResourceID == "" || input.DeploymentID == "" {
		return fmt.Errorf("%w: resourceId and deploymentId are required", ErrInvalidInput)
	}
	var target ManagedDeploymentRestarter
	switch input.Kind {
	case "redis":
		target = application.redis
	case "postgres":
		target = application.postgres
	default:
		return fmt.Errorf("%w: kind must be redis or postgres", ErrInvalidInput)
	}
	if restart {
		return target.RestartDeployment(ctx, input.ProjectID, input.ResourceID, input.DeploymentID)
	}
	return target.RemoveDeployment(ctx, input.ProjectID, input.ResourceID, input.DeploymentID)
}

// Ensure managed apps satisfy the restarter interface at compile time.
var (
	_ ManagedDeploymentRestarter = (*managedredis.Application)(nil)
	_ ManagedDeploymentRestarter = (*managedpostgres.Application)(nil)
)
