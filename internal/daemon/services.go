package daemon

import (
	"context"
	"fmt"

	"github.com/iivankin/platformd/internal/state"
)

type liveServiceRepository struct {
	store   *state.Store
	runtime serviceRuntime
}

type serviceRuntime interface {
	DeployService(context.Context, string, bool) error
	TrackService(context.Context, string, bool) error
	recordServiceFailure(string, error)
}

func (repository liveServiceRepository) Service(ctx context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	return repository.store.Service(ctx, projectID, serviceID)
}

func (repository liveServiceRepository) ServiceDeployments(ctx context.Context, projectID, serviceID, cursor string, limit int) (state.DeploymentPage, error) {
	return repository.store.ServiceDeployments(ctx, projectID, serviceID, cursor, limit)
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
		deployErr := repository.runtime.DeployService(ctx, created.ID, false)
		repository.finishReconcile(ctx, created.ID, deployErr)
	}
	return repository.store.DesiredService(ctx, created.ID)
}

func (repository liveServiceRepository) UpdateService(ctx context.Context, input state.UpdateServiceInput) (state.ServiceDesired, error) {
	updated, err := repository.store.UpdateService(ctx, input)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	deployErr := repository.runtime.DeployService(ctx, updated.ID, false)
	repository.finishReconcile(ctx, updated.ID, deployErr)
	return repository.store.DesiredService(ctx, updated.ID)
}

func (repository liveServiceRepository) RollbackService(ctx context.Context, input state.RollbackServiceInput) (state.ServiceDesired, error) {
	updated, err := repository.store.RollbackService(ctx, input)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	deployErr := repository.runtime.DeployService(ctx, updated.ID, true)
	repository.finishReconcile(ctx, updated.ID, deployErr)
	return repository.store.DesiredService(ctx, updated.ID)
}

func (repository liveServiceRepository) RedeployService(ctx context.Context, input state.RedeployServiceInput) (state.ServiceDesired, error) {
	service, err := repository.store.RedeployService(ctx, input)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	deployErr := repository.runtime.DeployService(ctx, service.ID, true)
	repository.finishReconcile(ctx, service.ID, deployErr)
	if deployErr != nil {
		return state.ServiceDesired{}, fmt.Errorf("%w: %v", state.ErrServiceReconcileFailed, deployErr)
	}
	return repository.store.DesiredService(ctx, service.ID)
}

func (repository liveServiceRepository) finishReconcile(ctx context.Context, serviceID string, deployErr error) {
	if trackErr := repository.runtime.TrackService(ctx, serviceID, deployErr != nil); trackErr != nil {
		repository.runtime.recordServiceFailure(serviceID, trackErr)
	}
}
