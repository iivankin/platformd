package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type liveServiceRepository struct {
	store            *state.Store
	runtime          serviceRuntime
	domains          *liveDomainRepository
	listeners        *liveServiceListenerRepository
	volumeFilesystem volume.Filesystem
	onCleanupError   func(error)
}

type serviceRuntime interface {
	DeployService(context.Context, string, bool) error
	DeployServiceRevision(context.Context, string, string, bool) error
	RestartServiceDeployment(context.Context, string, string) error
	DeleteServiceDeploymentLogs(string, string) error
	DeleteService(context.Context, state.ServiceDesired) error
	DeleteServiceLogs(string) error
	stopServicePreviews(context.Context, string, string) error
	TrackService(context.Context, string, bool) error
	recordServiceFailure(string, error)
}

func (repository liveServiceRepository) DeleteService(ctx context.Context, input state.DeleteServiceInput) (state.DeleteServiceResult, error) {
	service, err := repository.store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return state.DeleteServiceResult{}, err
	}
	if service.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return state.DeleteServiceResult{}, state.ErrServiceChanged
	}
	if err := repository.runtime.stopServicePreviews(ctx, service.ID, "Service deleted"); err != nil {
		return state.DeleteServiceResult{}, err
	}
	if err := repository.runtime.DeleteService(ctx, service); err != nil {
		return state.DeleteServiceResult{}, err
	}
	if repository.listeners != nil {
		if err := repository.listeners.WithdrawService(ctx, service.ID); err != nil {
			return state.DeleteServiceResult{}, errors.Join(err, repository.runtime.DeployService(ctx, service.ID, false))
		}
	}
	deleted, err := repository.store.DeleteService(ctx, input)
	if err != nil {
		var restoreListeners error
		if repository.listeners != nil {
			restoreListeners = repository.listeners.Restore(ctx)
		}
		return state.DeleteServiceResult{}, errors.Join(err, restoreListeners, repository.runtime.DeployService(ctx, service.ID, false))
	}
	if repository.domains != nil {
		repository.reportCleanupError(repository.domains.reload(ctx))
	}
	repository.reportCleanupError(repository.runtime.DeleteServiceLogs(service.ID))
	if repository.volumeFilesystem != nil {
		for _, item := range deleted.Volumes {
			repository.reportCleanupError(repository.volumeFilesystem.Remove(ctx, item.ProjectID, item.ID))
		}
	}
	return deleted, nil
}

func (repository liveServiceRepository) reportCleanupError(err error) {
	if err != nil && repository.onCleanupError != nil {
		repository.onCleanupError(err)
	}
}

func (repository liveServiceRepository) RestartServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	service, err := repository.store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	if service.UpdatedAtMillis != input.ExpectedUpdatedMillis || service.ActiveDeploymentID != input.DeploymentID || !service.Enabled {
		return state.ServiceDesired{}, state.ErrServiceChanged
	}
	if err := repository.runtime.RestartServiceDeployment(ctx, input.ID, input.DeploymentID); err != nil {
		return state.ServiceDesired{}, err
	}
	return repository.store.DesiredService(ctx, input.ID)
}

func (repository liveServiceRepository) RemoveServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	service, err := repository.store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	if service.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return state.ServiceDesired{}, state.ErrServiceChanged
	}
	if service.ActiveDeploymentID == input.DeploymentID {
		return repository.UpdateService(ctx, state.UpdateServiceInput{
			ID: service.ID, ProjectID: service.ProjectID, Enabled: false, Snapshot: service.Snapshot,
			ExpectedUpdatedMillis: service.UpdatedAtMillis,
			AuditEventID:          input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			RequestCorrelationID: input.RequestCorrelationID, UpdatedAtMillis: input.CreatedAtMillis,
		})
	}
	if _, err := repository.store.ServiceDeployment(ctx, input.ProjectID, input.ID, input.DeploymentID); err != nil {
		return state.ServiceDesired{}, err
	}
	if err := repository.runtime.DeleteServiceDeploymentLogs(input.ID, input.DeploymentID); err != nil {
		return state.ServiceDesired{}, err
	}
	if err := repository.store.DeleteServiceDeployment(ctx, input); err != nil {
		return state.ServiceDesired{}, err
	}
	return repository.store.DesiredService(ctx, input.ID)
}

func (repository liveServiceRepository) Service(ctx context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	return repository.store.Service(ctx, projectID, serviceID)
}

func (repository liveServiceRepository) ServiceDeployments(ctx context.Context, projectID, serviceID, cursor string, limit int) (state.DeploymentPage, error) {
	return repository.store.ServiceDeployments(ctx, projectID, serviceID, cursor, limit)
}

func (repository liveServiceRepository) ServiceDeployment(ctx context.Context, projectID, serviceID, deploymentID string) (state.DeploymentRecord, error) {
	deployment, err := repository.store.ServiceDeployment(ctx, projectID, serviceID, deploymentID)
	if !errors.Is(err, state.ErrDeploymentNotFound) {
		return deployment, err
	}
	preview, previewErr := repository.store.PreviewDeployment(ctx, projectID, serviceID, deploymentID)
	if previewErr != nil {
		return state.DeploymentRecord{}, previewErr
	}
	status := preview.Status
	if status == "active" || status == "building" {
		status = "running"
	} else if status == "stopped" {
		status = "interrupted"
	}
	return state.DeploymentRecord{
		ID: preview.ID, ServiceID: preview.ServiceID,
		ImageDigest: preview.ImageDigest, ImageReference: preview.ImageReference,
		SourceRevision: preview.SourceRevision, CommitMessage: preview.CommitMessage,
		ConfigHash: preview.ConfigHash, Snapshot: preview.Snapshot, Status: status,
		ErrorCode: preview.ErrorCode, ErrorMessage: preview.ErrorMessage,
		CreatedAtMillis: preview.CreatedAtMillis, FinishedAtMillis: preview.FinishedAtMillis,
	}, nil
}

func (repository liveServiceRepository) ServicePreviewDeployments(ctx context.Context, projectID, serviceID string) ([]state.PreviewDeployment, error) {
	return repository.store.PreviewDeploymentsForService(ctx, projectID, serviceID)
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
	if !updated.Enabled || updated.Snapshot.Source.GitHub == nil || updated.Snapshot.Source.GitHub.PullRequestPreview == nil {
		if err := repository.runtime.stopServicePreviews(ctx, updated.ID, "PR previews disabled"); err != nil {
			return state.ServiceDesired{}, err
		}
	}
	deployErr := repository.runtime.DeployService(ctx, updated.ID, false)
	repository.finishReconcile(ctx, updated.ID, deployErr)
	return repository.store.DesiredService(ctx, updated.ID)
}

func (repository liveServiceRepository) DeployServiceVersion(ctx context.Context, input state.DeployServiceVersionInput) (state.ServiceDesired, error) {
	deployment, err := repository.store.ServiceDeployment(ctx, input.ProjectID, input.ID, input.DeploymentID)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	updated, err := repository.store.DeployServiceVersion(ctx, input)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	var deployErr error
	if updated.Snapshot.Source.GitHub != nil {
		deployErr = repository.runtime.DeployServiceRevision(ctx, updated.ID, deployment.SourceRevision, true)
	} else {
		deployErr = repository.runtime.DeployService(ctx, updated.ID, true)
	}
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
