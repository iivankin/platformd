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
