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

func (repository liveProjectRepository) ProjectCanvas(ctx context.Context, projectID string) (state.ProjectCanvas, error) {
	canvas, err := repository.store.ProjectCanvas(ctx, projectID)
	if err != nil {
		return state.ProjectCanvas{}, err
	}
	for index := range canvas.Resources {
		resource := &canvas.Resources[index]
		switch resource.Kind {
		case "service":
			runtimeStatus, runtimeMessage := repository.runtime.ServiceStatus(resource.ID, resource.Enabled)
			if (runtimeStatus == "pending" && resource.Status == "failed") ||
				(runtimeStatus == "running" && resource.Status == "degraded") {
				continue
			}
			resource.Status, resource.StatusMessage = runtimeStatus, runtimeMessage
		case "redis":
			resource.Status, resource.StatusMessage = repository.runtime.RedisStatus(resource.ID)
		case "postgres":
			resource.Status, resource.StatusMessage = repository.runtime.PostgresStatus(resource.ID)
		case "object_store":
			resource.Status, resource.StatusMessage = repository.runtime.ObjectStoreStatus(canvas.Project.ID)
		case "network_gateway":
			resource.Status, resource.StatusMessage = repository.runtime.NetworkGatewayStatus(resource.ID)
		}
	}
	return canvas, nil
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
