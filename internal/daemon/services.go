package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/state"
)

type liveServiceRepository struct {
	store   *state.Store
	runtime *runtimeStack
}

func (repository liveServiceRepository) CreateService(ctx context.Context, input state.CreateService) (state.ServiceDesired, error) {
	created, err := repository.store.CreateService(ctx, input)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	if created.Enabled {
		// Desired state stays committed even when the first pull is temporarily
		// unavailable; watcher/reconcile retries registry errors without inventing
		// a durable job queue.
		_ = repository.runtime.DeployService(ctx, created.ID, false)
	}
	return repository.store.DesiredService(ctx, created.ID)
}
