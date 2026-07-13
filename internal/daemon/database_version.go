package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

type redisVersionAdapter struct {
	store   *state.Store
	runtime *runtimeStack
}

type postgresVersionAdapter struct {
	store   *state.Store
	runtime *runtimeStack
}

func (adapter postgresVersionAdapter) Resource(
	ctx context.Context,
	projectID string,
	resourceID string,
) (databaseversion.Resource, error) {
	resource, err := adapter.store.ManagedPostgresInProject(ctx, projectID, resourceID)
	if err != nil {
		return databaseversion.Resource{}, err
	}
	return databaseversion.Resource{
		ID: resource.ID, ProjectID: resource.ProjectID,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
	}, nil
}

func (adapter postgresVersionAdapter) Resolve(ctx context.Context, imageTag string) (string, error) {
	return adapter.runtime.ResolveManagedPostgresImage(ctx, imageTag)
}

func (adapter postgresVersionAdapter) Change(ctx context.Context, request databaseversion.ChangeRequest) error {
	return adapter.runtime.ChangeManagedPostgresVersion(ctx, managedpostgres.VersionChangeInput{
		ResourceID: request.Resource.ID, ImageTag: request.ImageTag, ImageDigest: request.ImageDigest,
		Actor: managedpostgres.Actor{
			Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
		},
		Progress: request.Progress,
	})
}

func (adapter redisVersionAdapter) Resource(
	ctx context.Context,
	projectID string,
	resourceID string,
) (databaseversion.Resource, error) {
	resource, err := adapter.store.ManagedRedisInProject(ctx, projectID, resourceID)
	if err != nil {
		return databaseversion.Resource{}, err
	}
	return databaseversion.Resource{
		ID: resource.ID, ProjectID: resource.ProjectID,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
	}, nil
}

func (adapter redisVersionAdapter) Resolve(ctx context.Context, imageTag string) (string, error) {
	return adapter.runtime.ResolveManagedRedisImage(ctx, imageTag)
}

func (adapter redisVersionAdapter) Change(ctx context.Context, request databaseversion.ChangeRequest) error {
	return adapter.runtime.ChangeManagedRedisVersion(ctx, managedredis.VersionChangeInput{
		ResourceID: request.Resource.ID, ImageTag: request.ImageTag, ImageDigest: request.ImageDigest,
		Actor: managedredis.Actor{
			Kind: request.Actor.Kind, ID: request.Actor.ID, Email: request.Actor.Email,
		},
		Progress: request.Progress,
	})
}
