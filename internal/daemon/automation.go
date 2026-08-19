//go:build !platformd_worker

package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
	"github.com/iivankin/platformd/internal/trafficmetrics"
	"github.com/iivankin/platformd/internal/volume"
)

type liveAutomationRepository struct {
	store            *state.Store
	runtime          *runtimeStack
	domains          *liveDomainRepository
	listeners        *liveServiceListenerRepository
	volumeFilesystem volume.Filesystem
	traffic          *trafficmetrics.Registry
	certificates     *origin.Selector
	telemetry        *telemetry.ServiceManager
	telemetryRoutes  *liveServiceTelemetryRepository
	hosts            hostServiceStatus
	onCleanupError   func(error)
}

func (repository liveAutomationRepository) services() liveServiceRepository {
	return liveServiceRepository{
		store: repository.store, runtime: repository.runtime, domains: repository.domains,
		listeners: repository.listeners, volumeFilesystem: repository.volumeFilesystem,
		traffic: repository.traffic, certificates: repository.certificates,
		telemetry: repository.telemetry, telemetryRoutes: repository.telemetryRoutes,
		onCleanupError: repository.onCleanupError,
	}
}

func (repository liveAutomationRepository) Projects(ctx context.Context) ([]state.ProjectSummary, error) {
	return repository.store.Projects(ctx)
}

func (repository liveAutomationRepository) CreateProjectByToken(ctx context.Context, input state.CreateProjectByToken) (state.ProjectSummary, error) {
	created, err := repository.store.CreateProjectByToken(ctx, input)
	if err != nil {
		return state.ProjectSummary{}, err
	}
	project := state.RuntimeProject{ID: created.ID, Name: created.Name}
	_ = repository.runtime.AddProject(project)
	publishHostProjects(repository.hosts, project)
	return created, nil
}

func (repository liveAutomationRepository) Project(ctx context.Context, projectID string) (state.ProjectSummary, error) {
	return repository.store.Project(ctx, projectID)
}

func (repository liveAutomationRepository) ProjectCanvas(ctx context.Context, projectID string) (state.ProjectCanvas, error) {
	return (liveProjectRepository{
		store: repository.store, runtime: repository.runtime, hosts: repository.hosts,
	}).ProjectCanvas(ctx, projectID)
}

func (repository liveAutomationRepository) Service(ctx context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	return repository.store.Service(ctx, projectID, serviceID)
}

func (repository liveAutomationRepository) ServiceDeployments(ctx context.Context, projectID, serviceID, cursor string, limit int) (state.DeploymentPage, error) {
	return repository.store.ServiceDeployments(ctx, projectID, serviceID, cursor, limit)
}

func (repository liveAutomationRepository) ManagedRedisInProject(ctx context.Context, projectID, resourceID string) (state.ManagedRedis, error) {
	return repository.store.ManagedRedisInProject(ctx, projectID, resourceID)
}

func (repository liveAutomationRepository) ManagedRedisByProject(ctx context.Context, projectID string) ([]state.ManagedRedis, error) {
	return repository.store.ManagedRedisByProject(ctx, projectID)
}

func (repository liveAutomationRepository) ManagedPostgresInProject(ctx context.Context, projectID, resourceID string) (state.ManagedPostgres, error) {
	return repository.store.ManagedPostgresInProject(ctx, projectID, resourceID)
}

func (repository liveAutomationRepository) ManagedPostgresByProject(ctx context.Context, projectID string) ([]state.ManagedPostgres, error) {
	return repository.store.ManagedPostgresByProject(ctx, projectID)
}

func (repository liveAutomationRepository) ObjectStoreInProject(ctx context.Context, projectID, resourceID string) (state.ObjectStore, error) {
	return repository.store.ObjectStoreInProject(ctx, projectID, resourceID)
}

func (repository liveAutomationRepository) ObjectStoresByProject(ctx context.Context, projectID string) ([]state.ObjectStore, error) {
	return repository.store.ObjectStoresByProject(ctx, projectID)
}

func (repository liveAutomationRepository) BackupHistory(ctx context.Context, query state.BackupHistoryQuery) ([]state.BackupRecord, error) {
	return repository.store.BackupHistory(ctx, query)
}

func (repository liveAutomationRepository) CreateService(ctx context.Context, input state.CreateService) (state.ServiceDesired, error) {
	return repository.services().CreateService(ctx, input)
}

func (repository liveAutomationRepository) UpdateService(ctx context.Context, input state.UpdateServiceInput) (state.ServiceDesired, error) {
	return repository.services().UpdateService(ctx, input)
}

func (repository liveAutomationRepository) RollbackService(ctx context.Context, input state.RollbackServiceInput) (state.ServiceDesired, error) {
	return repository.services().DeployServiceVersion(ctx, input)
}

func (repository liveAutomationRepository) RedeployService(ctx context.Context, input state.RedeployServiceInput) (state.ServiceDesired, error) {
	return repository.services().RedeployService(ctx, input)
}

func (repository liveAutomationRepository) DeleteService(ctx context.Context, input state.DeleteServiceInput) (state.DeleteServiceResult, error) {
	return repository.services().DeleteService(ctx, input)
}

func (repository liveAutomationRepository) RestartServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return repository.services().RestartServiceDeployment(ctx, input)
}

func (repository liveAutomationRepository) RemoveServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return repository.services().RemoveServiceDeployment(ctx, input)
}
