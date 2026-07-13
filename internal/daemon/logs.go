package daemon

import (
	"context"
	"io"

	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type liveLogRepository struct {
	store  *state.Store
	reader *containerlogs.Reader
}

func (repository liveLogRepository) DownloadServiceLogs(
	ctx context.Context,
	projectID string,
	query containerlogs.DownloadQuery,
	destination io.Writer,
) (containerlogs.DownloadResult, error) {
	if _, err := repository.store.Service(ctx, projectID, query.ServiceID); err != nil {
		return containerlogs.DownloadResult{}, err
	}
	return repository.reader.Download(ctx, query, destination)
}

func (repository liveLogRepository) ServiceLogs(ctx context.Context, projectID, serviceID, deploymentID, contains string, limit int) (containerlogs.Window, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return containerlogs.Window{}, err
	}
	return repository.reader.Read(ctx, containerlogs.Query{
		ServiceID: serviceID, DeploymentID: deploymentID, Contains: contains, Limit: limit,
	})
}

func (repository liveLogRepository) ServiceLogRevision(ctx context.Context, projectID, serviceID, deploymentID, contains string) (string, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return "", err
	}
	return repository.reader.Revision(ctx, containerlogs.Query{
		ServiceID: serviceID, DeploymentID: deploymentID, Contains: contains,
	})
}
