package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type liveRecoveryRepository struct {
	store    *state.Store
	progress *recoveryProgress
}

func (repository *liveRecoveryRepository) RecoveryStatus(ctx context.Context) (server.RecoveryStatus, error) {
	resources, err := repository.store.ControlResources(ctx)
	if err != nil {
		return server.RecoveryStatus{}, err
	}
	completed := make(map[string]recoveryResourceResult)
	for _, result := range repository.progress.results() {
		completed[recoveryResourceKey(result.ResourceKind, result.ResourceID)] = result
	}
	status := server.RecoveryStatus{LastError: repository.progress.failure()}
	appendResources := func(kind string, identifiers []string) {
		for _, resourceID := range identifiers {
			resource := server.RecoveryResource{
				ResourceKind: kind, ResourceID: resourceID, Status: "pending",
			}
			if result, exists := completed[recoveryResourceKey(kind, resourceID)]; exists {
				resource.Status = "restored"
				resource.GenerationID = result.GenerationID
				resource.SourceCompletedAt = result.SourceCompletedAt
				if result.Empty {
					resource.Status = "empty"
				}
			}
			status.Resources = append(status.Resources, resource)
		}
	}
	appendResources("registry", resources.RegistryRepositories)
	appendResources("object_store", resources.ObjectStores)
	appendResources("postgres", resources.Postgres)
	appendResources("redis", resources.Redis)
	appendResources("volume", resources.Volumes)
	return status, nil
}

func (repository *liveRecoveryRepository) RetryRecovery() {
	repository.progress.requestRetry()
}
