package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	DefaultDeploymentPageSize = 50
	MaximumDeploymentPageSize = 100
)

var (
	ErrDeploymentPageInvalid   = errors.New("deployment page is invalid")
	ErrDeploymentCursorInvalid = errors.New("deployment cursor is invalid")
)

type DeploymentPage struct {
	Deployments []DeploymentRecord
	NextCursor  string
}

func (store *Store) Service(ctx context.Context, projectID, serviceID string) (ServiceDesired, error) {
	service, err := store.DesiredService(ctx, serviceID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && service.ProjectID != projectID) {
		return ServiceDesired{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceDesired{}, err
	}
	return service, nil
}

func (store *Store) ServiceDeployments(ctx context.Context, projectID, serviceID, cursor string, limit int) (DeploymentPage, error) {
	if limit == 0 {
		limit = DefaultDeploymentPageSize
	}
	if limit < 1 || limit > MaximumDeploymentPageSize {
		return DeploymentPage{}, fmt.Errorf("%w: page size must be between 1 and 100", ErrDeploymentPageInvalid)
	}
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return DeploymentPage{}, err
	}
	var cursorCreated int64
	if cursor != "" {
		err := store.database.QueryRowContext(ctx, `
SELECT created_at FROM deployments WHERE id = ? AND service_id = ?`, cursor, serviceID).Scan(&cursorCreated)
		if errors.Is(err, sql.ErrNoRows) {
			return DeploymentPage{}, ErrDeploymentCursorInvalid
		}
		if err != nil {
			return DeploymentPage{}, fmt.Errorf("load deployment cursor: %w", err)
		}
	}
	rows, err := store.database.QueryContext(ctx, `
	SELECT id, service_id, image_digest, image_reference, source_revision,
	       source_commit_message, service_config_hash, snapshot_json, status,
       error_code, error_message, created_at, finished_at
FROM deployments
WHERE service_id = ? AND (? = '' OR created_at < ? OR (created_at = ? AND id < ?))
ORDER BY created_at DESC, id DESC
LIMIT ?`, serviceID, cursor, cursorCreated, cursorCreated, cursor, limit+1)
	if err != nil {
		return DeploymentPage{}, fmt.Errorf("list service deployments: %w", err)
	}
	defer rows.Close()
	deployments := make([]DeploymentRecord, 0, limit+1)
	for rows.Next() {
		deployment, err := scanDeploymentRecord(rows)
		if err != nil {
			return DeploymentPage{}, err
		}
		deployments = append(deployments, deployment)
	}
	if err := rows.Err(); err != nil {
		return DeploymentPage{}, fmt.Errorf("iterate service deployments: %w", err)
	}
	page := DeploymentPage{Deployments: deployments}
	if len(deployments) > limit {
		page.Deployments = deployments[:limit]
		page.NextCursor = page.Deployments[len(page.Deployments)-1].ID
	}
	return page, nil
}

func (store *Store) ServiceDeployment(ctx context.Context, projectID, serviceID, deploymentID string) (DeploymentRecord, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return DeploymentRecord{}, err
	}
	deployment, err := scanDeploymentRecord(store.database.QueryRowContext(ctx, `
	SELECT id, service_id, image_digest, image_reference, source_revision,
	       source_commit_message, service_config_hash, snapshot_json, status,
       error_code, error_message, created_at, finished_at
FROM deployments WHERE id = ? AND service_id = ?`, deploymentID, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return DeploymentRecord{}, ErrDeploymentNotFound
	}
	return deployment, err
}

type deploymentScanner interface {
	Scan(...any) error
}

func scanDeploymentRecord(scanner deploymentScanner) (DeploymentRecord, error) {
	var deployment DeploymentRecord
	var snapshotJSON string
	var errorCode sql.NullString
	var errorMessage sql.NullString
	var sourceRevision sql.NullString
	var commitMessage sql.NullString
	var finishedAt sql.NullInt64
	if err := scanner.Scan(
		&deployment.ID, &deployment.ServiceID, &deployment.ImageDigest, &deployment.ImageReference,
		&sourceRevision, &commitMessage, &deployment.ConfigHash,
		&snapshotJSON, &deployment.Status, &errorCode, &errorMessage,
		&deployment.CreatedAtMillis, &finishedAt,
	); err != nil {
		return DeploymentRecord{}, fmt.Errorf("scan deployment: %w", err)
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &deployment.Snapshot); err != nil {
		return DeploymentRecord{}, fmt.Errorf("decode deployment snapshot: %w", err)
	}
	deployment.ErrorCode = errorCode.String
	deployment.ErrorMessage = errorMessage.String
	deployment.SourceRevision = sourceRevision.String
	deployment.CommitMessage = commitMessage.String
	deployment.FinishedAtMillis = finishedAt.Int64
	return deployment, nil
}
