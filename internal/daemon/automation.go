package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/state"
)

type liveAutomationRepository struct {
	store   *state.Store
	runtime *runtimeStack
}

func (repository liveAutomationRepository) Projects(ctx context.Context) ([]state.ProjectSummary, error) {
	return repository.store.Projects(ctx)
}

func (repository liveAutomationRepository) Project(ctx context.Context, projectID string) (state.ProjectSummary, error) {
	return repository.store.Project(ctx, projectID)
}

func (repository liveAutomationRepository) ProjectCanvas(ctx context.Context, projectID string) (state.ProjectCanvas, error) {
	return (liveProjectRepository{store: repository.store, runtime: repository.runtime}).ProjectCanvas(ctx, projectID)
}

func (repository liveAutomationRepository) Service(ctx context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	return repository.store.Service(ctx, projectID, serviceID)
}

func (repository liveAutomationRepository) ServiceDeployments(ctx context.Context, projectID, serviceID, cursor string, limit int) (state.DeploymentPage, error) {
	return repository.store.ServiceDeployments(ctx, projectID, serviceID, cursor, limit)
}

func (repository liveAutomationRepository) CreateService(ctx context.Context, input state.CreateService) (state.ServiceDesired, error) {
	return (liveServiceRepository{store: repository.store, runtime: repository.runtime}).CreateService(ctx, input)
}

func (repository liveAutomationRepository) UpdateService(ctx context.Context, input state.UpdateServiceInput) (state.ServiceDesired, error) {
	return (liveServiceRepository{store: repository.store, runtime: repository.runtime}).UpdateService(ctx, input)
}

func (repository liveAutomationRepository) RollbackService(ctx context.Context, input state.RollbackServiceInput) (state.ServiceDesired, error) {
	return (liveServiceRepository{store: repository.store, runtime: repository.runtime}).RollbackService(ctx, input)
}

func (repository liveAutomationRepository) RedeployService(ctx context.Context, input state.RedeployServiceInput) (state.ServiceDesired, error) {
	return (liveServiceRepository{store: repository.store, runtime: repository.runtime}).RedeployService(ctx, input)
}
