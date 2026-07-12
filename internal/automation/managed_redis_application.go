package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/managedredis"
)

type ManagedRedisCreator interface {
	Create(context.Context, managedredis.CreateInput) (managedredis.CreateResult, error)
}

type ManagedRedisApplication struct {
	creator ManagedRedisCreator
}

type CreateManagedRedisInput struct {
	ProjectID     string
	Name          string
	ImageTag      string
	CPUMillicores int64
	MemoryBytes   int64
}

func NewManagedRedisApplication(creator ManagedRedisCreator) (*ManagedRedisApplication, error) {
	if creator == nil {
		return nil, errors.New("managed Redis automation creator is required")
	}
	return &ManagedRedisApplication{creator: creator}, nil
}

func (application *ManagedRedisApplication) Create(ctx context.Context, identity Identity, input CreateManagedRedisInput) (managedredis.CreateResult, error) {
	if identity.TokenID == "" || !identity.IsAdmin() {
		return managedredis.CreateResult{}, ErrAdminRequired
	}
	if input.ProjectID == "" {
		return managedredis.CreateResult{}, ErrInvalidInput
	}
	if !identity.AllowsProject(input.ProjectID) {
		return managedredis.CreateResult{}, ErrProjectBoundary
	}
	return application.creator.Create(ctx, managedredis.CreateInput{
		ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		CPUMillicores: input.CPUMillicores, MemoryBytes: input.MemoryBytes,
		Actor: managedredis.Actor{Kind: "token", ID: identity.TokenID},
	})
}
