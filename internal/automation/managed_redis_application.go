package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/managedredis"
)

type ManagedRedisCreator interface {
	Create(context.Context, managedredis.CreateInput) (managedredis.CreateResult, error)
}

type ManagedRedisBrowser interface {
	Keys(context.Context, string, string, managedredis.ScanQuery) (managedredis.KeyPage, error)
	Preview(context.Context, string, string, managedredis.PreviewQuery) (managedredis.Preview, error)
	Mutate(context.Context, managedredis.DataMutationInput) (managedredis.DataMutationResult, error)
}

type ManagedRedisApplication struct {
	creator ManagedRedisCreator
	browser ManagedRedisBrowser
}

type CreateManagedRedisInput struct {
	ProjectID     string
	Name          string
	ImageTag      string
	CPUMillicores int64
	MemoryBytes   int64
}

type ScanRedisKeysInput struct {
	ProjectID  string
	ResourceID string
	Cursor     uint64
	Match      string
	Count      int
}

type PreviewRedisKeyInput struct {
	ProjectID  string
	ResourceID string
	Key        string
	Count      int
}

type MutateRedisKeyInput struct {
	ProjectID  string
	ResourceID string
	Mutation   managedredis.Mutation
}

func NewManagedRedisApplication(creator ManagedRedisCreator) (*ManagedRedisApplication, error) {
	if creator == nil {
		return nil, errors.New("managed Redis automation creator is required")
	}
	browser, _ := creator.(ManagedRedisBrowser)
	return &ManagedRedisApplication{creator: creator, browser: browser}, nil
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

func (application *ManagedRedisApplication) Keys(ctx context.Context, identity Identity, input ScanRedisKeysInput) (managedredis.KeyPage, error) {
	if err := requireReadIdentity(identity); err != nil {
		return managedredis.KeyPage{}, err
	}
	if application.browser == nil {
		return managedredis.KeyPage{}, errors.New("managed Redis browser is unavailable")
	}
	if input.ProjectID == "" || input.ResourceID == "" {
		return managedredis.KeyPage{}, ErrInvalidInput
	}
	if !identity.AllowsProject(input.ProjectID) {
		return managedredis.KeyPage{}, ErrProjectBoundary
	}
	return application.browser.Keys(ctx, input.ProjectID, input.ResourceID, managedredis.ScanQuery{
		Cursor: input.Cursor, Match: input.Match, Count: input.Count,
	})
}

func (application *ManagedRedisApplication) Preview(ctx context.Context, identity Identity, input PreviewRedisKeyInput) (managedredis.Preview, error) {
	if err := requireReadIdentity(identity); err != nil {
		return managedredis.Preview{}, err
	}
	if application.browser == nil {
		return managedredis.Preview{}, errors.New("managed Redis browser is unavailable")
	}
	if input.ProjectID == "" || input.ResourceID == "" || input.Key == "" {
		return managedredis.Preview{}, ErrInvalidInput
	}
	if !identity.AllowsProject(input.ProjectID) {
		return managedredis.Preview{}, ErrProjectBoundary
	}
	return application.browser.Preview(ctx, input.ProjectID, input.ResourceID, managedredis.PreviewQuery{
		Key: []byte(input.Key), Count: input.Count,
	})
}

func (application *ManagedRedisApplication) Mutate(ctx context.Context, identity Identity, input MutateRedisKeyInput) (managedredis.DataMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return managedredis.DataMutationResult{}, err
	}
	if application.browser == nil {
		return managedredis.DataMutationResult{}, errors.New("managed Redis browser is unavailable")
	}
	if input.ResourceID == "" {
		return managedredis.DataMutationResult{}, ErrInvalidInput
	}
	return application.browser.Mutate(ctx, managedredis.DataMutationInput{
		ProjectID: input.ProjectID, ResourceID: input.ResourceID,
		Actor: managedredis.Actor{Kind: "token", ID: identity.TokenID}, Mutation: input.Mutation,
	})
}
