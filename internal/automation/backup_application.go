package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/state"
)

type BackupResource interface {
	SetPolicy(context.Context, backup.PolicyInput) (backup.PolicyResult, error)
	RunNow(context.Context, string, string, string) (state.BackupRecord, error)
	Restore(context.Context, string, string, string, string, backup.ResourceRestoreOptions, backup.Actor) (state.Operation, error)
}

type BackupOwnership interface {
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error)
}

type BackupApplication struct {
	resources BackupResource
	ownership BackupOwnership
}

type SetBackupPolicyInput struct {
	ProjectID      string
	Kind           string
	ResourceID     string
	TargetID       string
	Enabled        bool
	Cron           string
	RetentionCount int
}

type RunBackupInput struct {
	ProjectID  string
	Kind       string
	ResourceID string
	TargetID   string
}

type RestoreBackupInput struct {
	ProjectID            string
	Kind                 string
	ResourceID           string
	TargetID             string
	GenerationID         string
	Mode                 string
	NewResourceName      string
	DestructiveConfirmed bool
}

func NewBackupApplication(resources BackupResource, ownership BackupOwnership) (*BackupApplication, error) {
	if resources == nil || ownership == nil {
		return nil, errors.New("backup automation dependencies are incomplete")
	}
	return &BackupApplication{resources: resources, ownership: ownership}, nil
}

func (application *BackupApplication) SetPolicy(ctx context.Context, identity Identity, input SetBackupPolicyInput) (backup.PolicyResult, error) {
	if err := application.authorize(ctx, identity, input.ProjectID, input.Kind, input.ResourceID); err != nil {
		return backup.PolicyResult{}, err
	}
	return application.resources.SetPolicy(ctx, backup.PolicyInput{
		ResourceKind: input.Kind, ResourceID: input.ResourceID, TargetID: input.TargetID,
		Enabled: input.Enabled, Cron: input.Cron, RetentionCount: input.RetentionCount,
		Actor: backup.Actor{Kind: "token", ID: identity.TokenID},
	})
}

func (application *BackupApplication) RunNow(ctx context.Context, identity Identity, input RunBackupInput) (state.BackupRecord, error) {
	if err := application.authorize(ctx, identity, input.ProjectID, input.Kind, input.ResourceID); err != nil {
		return state.BackupRecord{}, err
	}
	return application.resources.RunNow(ctx, input.Kind, input.ResourceID, input.TargetID)
}

func (application *BackupApplication) Restore(ctx context.Context, identity Identity, input RestoreBackupInput) (state.Operation, error) {
	if err := application.authorize(ctx, identity, input.ProjectID, input.Kind, input.ResourceID); err != nil {
		return state.Operation{}, err
	}
	return application.resources.Restore(ctx, input.Kind, input.ResourceID, input.TargetID, input.GenerationID, backup.ResourceRestoreOptions{
		Mode: input.Mode, NewResourceName: input.NewResourceName, DestructiveConfirmed: input.DestructiveConfirmed,
	}, backup.Actor{Kind: "token", ID: identity.TokenID})
}

func (application *BackupApplication) authorize(ctx context.Context, identity Identity, projectID, kind, resourceID string) error {
	if err := authorizeServiceMutation(identity, projectID); err != nil {
		return err
	}
	if resourceID == "" {
		return fmt.Errorf("%w: resourceId is required", ErrInvalidInput)
	}
	switch kind {
	case "service":
		_, err := application.ownership.Service(ctx, projectID, resourceID)
		return err
	case "postgres":
		_, err := application.ownership.ManagedPostgresInProject(ctx, projectID, resourceID)
		return err
	case "redis":
		_, err := application.ownership.ManagedRedisInProject(ctx, projectID, resourceID)
		return err
	case "object_store":
		_, err := application.ownership.ObjectStoreInProject(ctx, projectID, resourceID)
		return err
	default:
		return fmt.Errorf("%w: kind must be service, postgres, redis, or object_store", ErrInvalidInput)
	}
}
