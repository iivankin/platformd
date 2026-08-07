package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/managedpostgres"
)

type ManagedPostgresCreator interface {
	Create(context.Context, managedpostgres.CreateInput) (managedpostgres.CreateResult, error)
}

type ManagedPostgresQuerier interface {
	Query(context.Context, managedpostgres.QueryInput) (managedpostgres.QueryOutput, error)
}

type ManagedPostgresApplication struct {
	creator ManagedPostgresCreator
	querier ManagedPostgresQuerier
}

type CreateManagedPostgresInput struct {
	ProjectID     string
	Name          string
	ImageTag      string
	CPUMillicores int64
	MemoryBytes   int64
}

type QueryManagedPostgresInput struct {
	ProjectID  string
	ResourceID string
	SQL        string
}

func NewManagedPostgresApplication(creator ManagedPostgresCreator) (*ManagedPostgresApplication, error) {
	if creator == nil {
		return nil, errors.New("managed PostgreSQL automation creator is required")
	}
	querier, _ := creator.(ManagedPostgresQuerier)
	return &ManagedPostgresApplication{creator: creator, querier: querier}, nil
}

func (application *ManagedPostgresApplication) Create(ctx context.Context, identity Identity, input CreateManagedPostgresInput) (managedpostgres.CreateResult, error) {
	if identity.TokenID == "" || !identity.IsAdmin() {
		return managedpostgres.CreateResult{}, ErrAdminRequired
	}
	if input.ProjectID == "" {
		return managedpostgres.CreateResult{}, ErrInvalidInput
	}
	if !identity.AllowsProject(input.ProjectID) {
		return managedpostgres.CreateResult{}, ErrProjectBoundary
	}
	return application.creator.Create(ctx, managedpostgres.CreateInput{
		ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		CPUMillicores: input.CPUMillicores, MemoryBytes: input.MemoryBytes,
		Actor: managedpostgres.Actor{Kind: "token", ID: identity.TokenID},
	})
}

func (application *ManagedPostgresApplication) Query(ctx context.Context, identity Identity, input QueryManagedPostgresInput) (managedpostgres.QueryOutput, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return managedpostgres.QueryOutput{}, err
	}
	if application.querier == nil {
		return managedpostgres.QueryOutput{}, errors.New("managed PostgreSQL query is unavailable")
	}
	if input.ResourceID == "" || input.SQL == "" {
		return managedpostgres.QueryOutput{}, ErrInvalidInput
	}
	return application.querier.Query(ctx, managedpostgres.QueryInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID,
		Actor: managedpostgres.Actor{Kind: "token", ID: identity.TokenID}, SQL: input.SQL,
	})
}
