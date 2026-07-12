package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/managedpostgres"
)

type ManagedPostgresCreator interface {
	Create(context.Context, managedpostgres.CreateInput) (managedpostgres.CreateResult, error)
}

type ManagedPostgresApplication struct {
	creator ManagedPostgresCreator
}

type CreateManagedPostgresInput struct {
	ProjectID     string
	Name          string
	ImageTag      string
	CPUMillicores int64
	MemoryBytes   int64
}

func NewManagedPostgresApplication(creator ManagedPostgresCreator) (*ManagedPostgresApplication, error) {
	if creator == nil {
		return nil, errors.New("managed PostgreSQL automation creator is required")
	}
	return &ManagedPostgresApplication{creator: creator}, nil
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
