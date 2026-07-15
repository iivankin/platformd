package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	DefaultRuntimeDeploymentPageSize = 50
	MaximumRuntimeDeploymentPageSize = 100
)

var (
	ErrRuntimeDeploymentNotFound = errors.New("runtime deployment not found")
	ErrRuntimeDeploymentInvalid  = errors.New("runtime deployment is invalid")
)

type RuntimeDeployment struct {
	ID               string
	ResourceKind     string
	ResourceID       string
	ImageTag         string
	ImageDigest      string
	Status           string
	Active           bool
	ErrorCode        string
	ErrorMessage     string
	CreatedAtMillis  int64
	FinishedAtMillis int64
}

type RuntimeDeploymentPage struct {
	Deployments []RuntimeDeployment
	NextCursor  string
}

func (store *Store) BeginRuntimeDeployment(ctx context.Context, deployment RuntimeDeployment) error {
	if deployment.ID == "" || deployment.ResourceID == "" || deployment.ImageTag == "" || deployment.ImageDigest == "" || deployment.CreatedAtMillis <= 0 || (deployment.ResourceKind != "postgres" && deployment.ResourceKind != "redis") {
		return ErrRuntimeDeploymentInvalid
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO runtime_deployments(
  id, resource_kind, resource_id, image_tag, image_digest, status, active, created_at
) VALUES (?, ?, ?, ?, ?, 'running', 0, ?)`,
			deployment.ID, deployment.ResourceKind, deployment.ResourceID,
			deployment.ImageTag, deployment.ImageDigest, deployment.CreatedAtMillis,
		)
		return err
	})
}

func (store *Store) ActivateRuntimeDeployment(ctx context.Context, kind, resourceID, deploymentID string, finishedAtMillis int64) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		if _, err := transaction.ExecContext(ctx, `
UPDATE runtime_deployments SET active = 0
WHERE resource_kind = ? AND resource_id = ? AND active = 1`, kind, resourceID); err != nil {
			return fmt.Errorf("clear active runtime deployment: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE runtime_deployments
SET status = 'succeeded', active = 1, error_code = NULL, error_message = NULL, finished_at = ?
WHERE id = ? AND resource_kind = ? AND resource_id = ?`, finishedAtMillis, deploymentID, kind, resourceID)
		if err != nil {
			return fmt.Errorf("activate runtime deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrRuntimeDeploymentNotFound
		}
		return nil
	})
}

func (store *Store) FailRuntimeDeployment(ctx context.Context, deploymentID, code, message string, finishedAtMillis int64) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE runtime_deployments
SET status = 'failed', error_code = ?, error_message = ?, finished_at = ?
WHERE id = ?`, code, message, finishedAtMillis, deploymentID)
		if err != nil {
			return fmt.Errorf("fail runtime deployment: %w", err)
		}
		return requireOneRuntimeDeployment(result)
	})
}

// StopRuntimeDeployment marks the current deployment as removed without
// deleting its volume or history. It remains the current deployment so the
// same version and data can be restarted later.
func (store *Store) StopRuntimeDeployment(ctx context.Context, kind, resourceID, deploymentID string, finishedAtMillis int64) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE runtime_deployments
SET status = 'removed', error_code = NULL, error_message = NULL, finished_at = ?
WHERE id = ? AND resource_kind = ? AND resource_id = ? AND active = 1`,
			finishedAtMillis, deploymentID, kind, resourceID)
		if err != nil {
			return err
		}
		return requireOneRuntimeDeployment(result)
	})
}

// DeleteRuntimeDeployment permanently removes an old history record. The
// current deployment must first be superseded; stopping it is intentionally a
// separate operation because its volume stays restartable.
func (store *Store) DeleteRuntimeDeployment(ctx context.Context, kind, resourceID, deploymentID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM runtime_deployments
WHERE id = ? AND resource_kind = ? AND resource_id = ? AND active = 0`, deploymentID, kind, resourceID)
		if err != nil {
			return err
		}
		return requireOneRuntimeDeployment(result)
	})
}

func (store *Store) RestartRuntimeDeployment(ctx context.Context, kind, resourceID, deploymentID string) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE runtime_deployments
SET status = 'running', error_code = NULL, error_message = NULL, finished_at = NULL
WHERE id = ? AND resource_kind = ? AND resource_id = ? AND active = 1`, deploymentID, kind, resourceID)
		if err != nil {
			return err
		}
		return requireOneRuntimeDeployment(result)
	})
}

func (store *Store) ActiveRuntimeDeployment(ctx context.Context, kind, resourceID string) (RuntimeDeployment, error) {
	return scanRuntimeDeployment(store.database.QueryRowContext(ctx, `
SELECT id, resource_kind, resource_id, image_tag, image_digest, status, active,
       error_code, error_message, created_at, finished_at
FROM runtime_deployments
WHERE resource_kind = ? AND resource_id = ? AND active = 1`, kind, resourceID))
}

func (store *Store) RuntimeDeployment(ctx context.Context, kind, resourceID, deploymentID string) (RuntimeDeployment, error) {
	return scanRuntimeDeployment(store.database.QueryRowContext(ctx, `
SELECT id, resource_kind, resource_id, image_tag, image_digest, status, active,
       error_code, error_message, created_at, finished_at
FROM runtime_deployments
WHERE id = ? AND resource_kind = ? AND resource_id = ?`, deploymentID, kind, resourceID))
}

func (store *Store) RuntimeDeployments(ctx context.Context, kind, resourceID, cursor string, limit int) (RuntimeDeploymentPage, error) {
	if resourceID == "" || (kind != "postgres" && kind != "redis") {
		return RuntimeDeploymentPage{}, ErrRuntimeDeploymentInvalid
	}
	if limit == 0 {
		limit = DefaultRuntimeDeploymentPageSize
	}
	if limit < 1 || limit > MaximumRuntimeDeploymentPageSize {
		return RuntimeDeploymentPage{}, ErrDeploymentPageInvalid
	}
	var cursorCreated int64
	if cursor != "" {
		if err := store.database.QueryRowContext(ctx, `SELECT created_at FROM runtime_deployments WHERE id = ? AND resource_kind = ? AND resource_id = ?`, cursor, kind, resourceID).Scan(&cursorCreated); errors.Is(err, sql.ErrNoRows) {
			return RuntimeDeploymentPage{}, ErrDeploymentCursorInvalid
		} else if err != nil {
			return RuntimeDeploymentPage{}, err
		}
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, resource_kind, resource_id, image_tag, image_digest, status, active,
       error_code, error_message, created_at, finished_at
FROM runtime_deployments
WHERE resource_kind = ? AND resource_id = ?
  AND (? = '' OR created_at < ? OR (created_at = ? AND id < ?))
ORDER BY created_at DESC, id DESC LIMIT ?`, kind, resourceID, cursor, cursorCreated, cursorCreated, cursor, limit+1)
	if err != nil {
		return RuntimeDeploymentPage{}, err
	}
	defer rows.Close()
	items := make([]RuntimeDeployment, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanRuntimeDeployment(rows)
		if scanErr != nil {
			return RuntimeDeploymentPage{}, scanErr
		}
		items = append(items, item)
	}
	page := RuntimeDeploymentPage{Deployments: items}
	if len(items) > limit {
		page.Deployments = items[:limit]
		page.NextCursor = page.Deployments[len(page.Deployments)-1].ID
	}
	return page, rows.Err()
}

func requireOneRuntimeDeployment(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrRuntimeDeploymentNotFound
	}
	return nil
}

type runtimeDeploymentScanner interface{ Scan(...any) error }

func scanRuntimeDeployment(scanner runtimeDeploymentScanner) (RuntimeDeployment, error) {
	var item RuntimeDeployment
	var active int
	var errorCode, errorMessage sql.NullString
	var finishedAt sql.NullInt64
	if err := scanner.Scan(
		&item.ID, &item.ResourceKind, &item.ResourceID, &item.ImageTag, &item.ImageDigest,
		&item.Status, &active, &errorCode, &errorMessage, &item.CreatedAtMillis, &finishedAt,
	); errors.Is(err, sql.ErrNoRows) {
		return RuntimeDeployment{}, ErrRuntimeDeploymentNotFound
	} else if err != nil {
		return RuntimeDeployment{}, err
	}
	item.Active = active == 1
	item.ErrorCode = errorCode.String
	item.ErrorMessage = errorMessage.String
	item.FinishedAtMillis = finishedAt.Int64
	return item, nil
}
