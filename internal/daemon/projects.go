package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/state"
)

type liveProjectRepository struct {
	store   *state.Store
	runtime *runtimeStack
}

func (repository liveProjectRepository) Projects(ctx context.Context) ([]state.ProjectSummary, error) {
	return repository.store.Projects(ctx)
}

func (repository liveProjectRepository) CreateProject(ctx context.Context, input state.CreateProject) (state.ProjectSummary, error) {
	created, err := repository.store.CreateProject(ctx, input)
	if err != nil {
		return state.ProjectSummary{}, err
	}
	// Desired state is already committed. Runtime provisioning is best-effort
	// and remains retryable from SQLite after a process restart.
	_ = repository.runtime.AddProject(state.RuntimeProject{ID: created.ID, Name: created.Name})
	return created, nil
}
