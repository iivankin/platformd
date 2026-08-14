package daemon

import (
	"context"
	"fmt"
	"io"

	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type liveLogRepository struct {
	store           *state.Store
	telemetryReader telemetryLogReader
}

type telemetryLogReader interface {
	Download(context.Context, containerlogs.DownloadQuery, io.Writer) (containerlogs.DownloadResult, error)
	Read(context.Context, containerlogs.Query) (containerlogs.Window, error)
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
	return repository.telemetryReader.Download(ctx, query, destination)
}

func (repository liveLogRepository) ResourceLogs(ctx context.Context, projectID string, query containerlogs.ResourceQuery) (containerlogs.Window, error) {
	switch query.Kind {
	case "service":
		if _, err := repository.store.Service(ctx, projectID, query.ResourceID); err != nil {
			return containerlogs.Window{}, err
		}
	case "postgres":
		if _, err := repository.store.ManagedPostgresInProject(ctx, projectID, query.ResourceID); err != nil {
			return containerlogs.Window{}, err
		}
	case "redis":
		if _, err := repository.store.ManagedRedisInProject(ctx, projectID, query.ResourceID); err != nil {
			return containerlogs.Window{}, err
		}
	default:
		return containerlogs.Window{}, fmt.Errorf("%w: unsupported resource log kind", containerlogs.ErrInvalidQuery)
	}
	query.Query.ServiceID = query.ResourceID
	return repository.telemetryReader.Read(ctx, query.Query)
}
