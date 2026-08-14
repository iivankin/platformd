package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type liveProjectRepository struct {
	store            *state.Store
	runtime          *runtimeStack
	backups          *backup.ResourceApplication
	domains          *liveDomainRepository
	objectStores     *liveObjectStoreRepository
	objectStoreData  objectStoreDataCleaner
	serviceTelemetry *telemetry.ServiceManager
	telemetryRoutes  *liveServiceTelemetryRepository
	listeners        *liveServiceListenerRepository
	gateways         *liveNetworkGatewayRepository
	traffic          *trafficmetrics.Registry
	onCleanupError   func(error)
}

type objectStoreDataCleaner interface {
	DeleteStoreData(context.Context, string) error
}

const objectStoreCleanupTimeout = 30 * time.Second

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

func (repository liveProjectRepository) ProjectIcon(ctx context.Context, projectID string) (state.ProjectIcon, error) {
	return repository.store.ProjectIcon(ctx, projectID)
}

func (repository liveProjectRepository) SetProjectIcon(ctx context.Context, input state.SetProjectIconInput) (state.ProjectSummary, error) {
	return repository.store.SetProjectIcon(ctx, input)
}

func (repository liveProjectRepository) ClearProjectIcon(ctx context.Context, input state.ClearProjectIconInput) (state.ProjectSummary, error) {
	return repository.store.ClearProjectIcon(ctx, input)
}

func (repository liveProjectRepository) DeleteProject(ctx context.Context, input state.DeleteProjectInput) (state.ProjectDeletionPlan, error) {
	plan, err := repository.store.ProjectDeletionPlan(ctx, input.ID)
	if err != nil {
		return state.ProjectDeletionPlan{}, err
	}
	if plan.Project.Name != input.ExpectedName {
		return state.ProjectDeletionPlan{}, state.ErrProjectChanged
	}
	if input.DeleteBackups {
		if repository.backups == nil {
			return state.ProjectDeletionPlan{}, errors.New("resource backup deletion is not available")
		}
		resources := make([]backup.ResourceIdentity, 0)
		for _, resource := range plan.BackupResources() {
			resources = append(resources, backup.ResourceIdentity{Kind: resource.Kind, ID: resource.ID})
		}
		if err := repository.backups.PurgeResources(ctx, resources); err != nil {
			return state.ProjectDeletionPlan{}, err
		}
	}
	for _, service := range plan.Services {
		if err := repository.runtime.stopServicePreviews(ctx, service.ID, "Project deleted"); err != nil {
			return state.ProjectDeletionPlan{}, err
		}
		if err := repository.runtime.deleteServiceDuringProjectDeletion(ctx, service); err != nil {
			return state.ProjectDeletionPlan{}, err
		}
		if repository.listeners != nil {
			if err := repository.listeners.WithdrawService(ctx, service.ID); err != nil {
				return state.ProjectDeletionPlan{}, err
			}
		}
	}
	if repository.gateways != nil {
		if err := repository.gateways.WithdrawProject(plan.Gateways); err != nil {
			return state.ProjectDeletionPlan{}, err
		}
	}
	if err := repository.runtime.stopProjectDatabases(ctx, plan.Postgres, plan.Redis); err != nil {
		return state.ProjectDeletionPlan{}, err
	}
	var deletedTelemetryHostnames []string
	if repository.telemetryRoutes != nil {
		deletedTelemetryHostnames, err = repository.telemetryRoutes.deleteProjectDNS(ctx, plan.Services)
		if err != nil {
			return state.ProjectDeletionPlan{}, err
		}
	}
	restoreTelemetryHostnames := func(cause error) error {
		if repository.telemetryRoutes == nil {
			return cause
		}
		return errors.Join(cause, repository.telemetryRoutes.restoreDNS(ctx, deletedTelemetryHostnames))
	}
	deleted, err := repository.store.DeleteProject(ctx, input)
	if err != nil {
		return state.ProjectDeletionPlan{}, restoreTelemetryHostnames(err)
	}
	// Keep the Rust project endpoint alive while its per-store maintenance gates
	// drain requests and delete the physical buckets. Removing it first would
	// make DeleteStoreData unable to acquire the restore gate.
	repository.cleanupObjectStoreData(deleted.ObjectStores)
	for _, service := range deleted.Services {
		if repository.serviceTelemetry != nil {
			repository.reportCleanupError(cleanupServiceTelemetry(repository.serviceTelemetry, service))
		}
		repository.traffic.Forget(service.ID)
	}
	repository.reportCleanupError(repository.runtime.RemoveProject(input.ID))
	if repository.domains != nil {
		repository.reportCleanupError(repository.domains.reload(ctx))
	}
	if repository.objectStores != nil {
		repository.reportCleanupError(repository.objectStores.reloadPublicRoutes(ctx))
	}
	if repository.telemetryRoutes != nil {
		repository.reportCleanupError(repository.telemetryRoutes.reloadPublicRoutes(ctx))
	}
	repository.cleanupProjectFiles(deleted)
	return deleted, nil
}

func (repository liveProjectRepository) cleanupProjectFiles(plan state.ProjectDeletionPlan) {
	if filepath.Base(plan.Project.ID) != plan.Project.ID {
		repository.reportCleanupError(errors.New("project cleanup identity is invalid"))
		return
	}
	volumeCleanupErr := removeProjectManagedVolumes(context.Background(), repository.runtime.engine, plan)
	repository.reportCleanupError(volumeCleanupErr)
	if volumeCleanupErr == nil {
		repository.reportCleanupError(os.RemoveAll(filepath.Join(repository.runtime.paths.VolumesRoot, plan.Project.ID)))
	}
}

func (repository liveProjectRepository) cleanupObjectStoreData(stores []state.ObjectStore) {
	for _, resource := range stores {
		if repository.objectStoreData != nil {
			ctx, cancel := context.WithTimeout(context.Background(), objectStoreCleanupTimeout)
			err := repository.objectStoreData.DeleteStoreData(ctx, resource.ID)
			cancel()
			repository.reportCleanupError(err)
		}
	}
}

type projectManagedVolumeRuntime interface {
	RemoveManagedVolume(context.Context, string) error
}

func removeProjectManagedVolumes(
	ctx context.Context,
	runtime projectManagedVolumeRuntime,
	plan state.ProjectDeletionPlan,
) error {
	var failures []error
	remove := func(kind, volumeID string) {
		if err := runtime.RemoveManagedVolume(ctx, volumeID); err != nil {
			failures = append(failures, fmt.Errorf("remove %s runtime volume %s: %w", kind, volumeID, err))
		}
	}
	for _, stored := range plan.Volumes {
		remove("service", stored.ID)
	}
	for _, resource := range plan.Postgres {
		remove("PostgreSQL", resource.VolumeID)
	}
	for _, resource := range plan.Redis {
		remove("Redis", resource.VolumeID)
	}
	return errors.Join(failures...)
}

func (repository liveProjectRepository) reportCleanupError(err error) {
	if err != nil && repository.onCleanupError != nil {
		repository.onCleanupError(err)
	}
}
