package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/buildlog"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

const objectStoreLogLimit = state.MaximumAuditPageSize

type liveLogRepository struct {
	store  *state.Store
	reader *containerlogs.Reader
	root   string
}

func (repository liveLogRepository) BuildLog(ctx context.Context, projectID, serviceID, deploymentID string) (string, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return "", err
	}
	if _, err := repository.store.ServiceDeployment(ctx, projectID, serviceID, deploymentID); err != nil {
		if !errors.Is(err, state.ErrDeploymentNotFound) {
			return "", err
		}
		if _, previewErr := repository.store.PreviewDeployment(ctx, projectID, serviceID, deploymentID); previewErr != nil {
			return "", previewErr
		}
	}
	path := filepath.Join(repository.root, "services", serviceID, deploymentID, "build.log")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open build log: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, buildlog.MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read build log: %w", err)
	}
	if len(content) > buildlog.MaxBytes {
		content = append(content[:buildlog.MaxBytes-len(buildlog.TruncationMarker)], buildlog.TruncationMarker...)
	}
	return string(content), nil
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

func (repository liveLogRepository) ResourceLogs(ctx context.Context, projectID, kind, resourceID, deploymentID, contains string, limit int) (containerlogs.Window, error) {
	switch kind {
	case "postgres":
		if _, err := repository.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
			return containerlogs.Window{}, err
		}
	case "redis":
		if _, err := repository.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
			return containerlogs.Window{}, err
		}
	case "object_store":
		if _, err := repository.store.ObjectStoreInProject(ctx, projectID, resourceID); err != nil {
			return containerlogs.Window{}, err
		}
		return repository.objectStoreLogs(ctx, resourceID, contains, limit)
	default:
		return containerlogs.Window{}, fmt.Errorf("%w: unsupported resource log kind", containerlogs.ErrInvalidQuery)
	}
	return repository.reader.ReadRuntime(ctx, containerlogs.RuntimeQuery{
		Kind: kind, ResourceID: resourceID, DeploymentID: deploymentID, Contains: contains, Limit: limit,
	})
}

func (repository liveLogRepository) objectStoreLogs(ctx context.Context, resourceID, contains string, limit int) (containerlogs.Window, error) {
	if limit == 0 {
		limit = containerlogs.DefaultLimit
	}
	if limit < 1 || limit > containerlogs.MaximumLimit || len(contains) > containerlogs.MaximumContainsBytes || strings.ContainsRune(contains, '\x00') {
		return containerlogs.Window{}, containerlogs.ErrInvalidQuery
	}
	// Object storage runs in-process, so its resource log surface is the
	// authoritative audit activity stream rather than container log files.
	page, err := repository.store.AuditEvents(ctx, state.AuditQuery{
		TargetKind: "object_store", TargetID: resourceID, Limit: min(limit, objectStoreLogLimit),
	})
	if err != nil {
		return containerlogs.Window{}, err
	}
	records := make([]containerlogs.Record, 0, len(page.Events))
	for index := len(page.Events) - 1; index >= 0; index-- {
		event := page.Events[index]
		text := event.Action + " " + event.Result
		if contains != "" && !strings.Contains(text, contains) {
			continue
		}
		stream := "stdout"
		if event.Result == "failed" {
			stream = "stderr"
		}
		records = append(records, containerlogs.Record{
			Timestamp: time.UnixMilli(event.CreatedAtMillis), Stream: stream, Text: text,
			DeploymentID: resourceID, AttemptID: event.ID,
		})
	}
	return containerlogs.Window{Records: records, Truncated: page.NextCursor != ""}, nil
}
