package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

var ErrServiceChanged = errors.New("service changed during deployment")

type DeploymentRecord struct {
	ID               string
	ServiceID        string
	ImageDigest      string
	ImageReference   string
	ImageRevisionID  string
	SourceRevision   string
	CommitMessage    string
	ConfigHash       string
	Snapshot         serviceconfig.Snapshot
	Status           string
	ErrorCode        string
	ErrorMessage     string
	CreatedAtMillis  int64
	FinishedAtMillis int64
}

type BeginDeployment struct {
	ID               string
	ServiceID        string
	ImageDigest      string
	ImageReference   string
	ImageRevisionID  string
	SourceRevision   string
	CommitMessage    string
	ConfigHash       string
	SnapshotJSON     []byte
	CreatedAtMillis  int64
	Status           string
	FinishedAtMillis int64
}

func (store *Store) BeginDeployment(ctx context.Context, input BeginDeployment) error {
	if input.ID == "" || input.ServiceID == "" || input.ConfigHash == "" || len(input.SnapshotJSON) == 0 || input.CreatedAtMillis <= 0 {
		return errors.New("begin deployment input is incomplete")
	}
	status := input.Status
	if status == "" {
		status = "running"
	}
	if status != "running" && status != "skipped" {
		return errors.New("deployment can only begin as running or skipped")
	}
	if status == "skipped" && input.FinishedAtMillis <= 0 {
		return errors.New("skipped deployment must have a finish time")
	}
	var snapshot serviceconfig.Snapshot
	if err := json.Unmarshal(input.SnapshotJSON, &snapshot); err != nil {
		return fmt.Errorf("decode deployment snapshot: %w", err)
	}
	_, canonicalJSON, hash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		return err
	}
	if hash != input.ConfigHash || string(canonicalJSON) != string(input.SnapshotJSON) {
		return errors.New("deployment snapshot is not canonical or does not match its hash")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var finishedAt any
		if input.FinishedAtMillis > 0 {
			finishedAt = input.FinishedAtMillis
		}
		var enabled int
		if err := transaction.QueryRowContext(ctx, "SELECT enabled FROM services WHERE id = ?", input.ServiceID).Scan(&enabled); errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		} else if err != nil {
			return fmt.Errorf("load deployment service: %w", err)
		}
		if enabled != 1 {
			return ErrServiceChanged
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO deployments(
		  id, service_id, image_digest, image_reference, image_revision_id, source_revision,
		  source_commit_message, service_config_hash, snapshot_json, status, created_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ServiceID, input.ImageDigest, input.ImageReference,
			nullableString(input.ImageRevisionID),
			nullableString(input.SourceRevision), nullableString(input.CommitMessage),
			input.ConfigHash, string(input.SnapshotJSON), status, input.CreatedAtMillis,
			finishedAt,
		); err != nil {
			return fmt.Errorf("begin deployment: %w", err)
		}
		return nil
	})
}

func (store *Store) ActivateDeployment(ctx context.Context, serviceID, deploymentID, expectedActiveDeploymentID string, finishedAtMillis int64) error {
	if serviceID == "" || deploymentID == "" || finishedAtMillis <= 0 {
		return errors.New("activate deployment input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var activeDeploymentID sql.NullString
		var imageRevisionID sql.NullString
		var status string
		err := transaction.QueryRowContext(ctx, `
SELECT s.active_deployment_id, d.status, d.image_revision_id
FROM services s JOIN deployments d ON d.id = ? AND d.service_id = s.id
WHERE s.id = ? AND s.enabled = 1`, deploymentID, serviceID).Scan(&activeDeploymentID, &status, &imageRevisionID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServiceChanged
		}
		if err != nil {
			return fmt.Errorf("validate deployment activation: %w", err)
		}
		if status != "running" || activeDeploymentID.String != expectedActiveDeploymentID {
			return ErrServiceChanged
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET status = 'retired', retired_at = ?
WHERE service_id = ? AND kind = 'production' AND status = 'active' AND id IS NOT ?`,
			finishedAtMillis, serviceID, nullableString(imageRevisionID.String)); err != nil {
			return fmt.Errorf("retire previous uploaded image: %w", err)
		}
		if imageRevisionID.Valid {
			// failed is included because older deploys marked imported archives as
			// failed; a later deploy must still be able to publish that image.
			// Require a digest so import failures without a usable archive stay blocked.
			result, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions
SET status = 'active', deployment_id = ?, activated_at = ?, retired_at = NULL, expires_at = NULL
				WHERE id = ? AND service_id = ? AND kind = 'production'
  AND status IN ('importing', 'active', 'retired', 'failed')
  AND IFNULL(image_digest, '') != ''`,
				deploymentID, finishedAtMillis, imageRevisionID.String, serviceID)
			if err != nil {
				return fmt.Errorf("activate uploaded image revision: %w", err)
			}
			if changed, err := result.RowsAffected(); err != nil || changed != 1 {
				return ErrServiceChanged
			}
			if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_uploads SET deployment_id = ?
WHERE image_revision_id = ? AND status = 'deploying'`, deploymentID, imageRevisionID.String); err != nil {
				return fmt.Errorf("link uploaded image deployment: %w", err)
			}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE services SET active_deployment_id = ?, updated_at = ? WHERE id = ?`,
			deploymentID, finishedAtMillis, serviceID); err != nil {
			return fmt.Errorf("publish active deployment: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE deployments SET status = 'succeeded', finished_at = ?
WHERE id = ? AND status = 'running'`, finishedAtMillis, deploymentID); err != nil {
			return fmt.Errorf("complete deployment: %w", err)
		}
		return nil
	})
}

// DiscardDeployment removes a resolved no-op from history. The active pointer
// guard makes this safe even if deployment state changes concurrently.
func (store *Store) DiscardDeployment(ctx context.Context, deploymentID string) error {
	if deploymentID == "" {
		return errors.New("deployment ID is required")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM deployments
WHERE id = ? AND status = 'running'
  AND id NOT IN (
    SELECT active_deployment_id FROM services WHERE active_deployment_id IS NOT NULL
  )`, deploymentID)
		if err != nil {
			return fmt.Errorf("discard no-op deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count discarded no-op deployment: %w", err)
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

func (store *Store) FailDeployment(ctx context.Context, deploymentID, code, message string, finishedAtMillis int64) error {
	return store.FinishDeployment(ctx, deploymentID, "failed", code, message, finishedAtMillis)
}

// UpdateDeploymentSource fills the immutable source identity once a pull or
// build has resolved it. The deployment row is created before that potentially
// slow operation so clients can observe the running attempt immediately.
func (store *Store) UpdateDeploymentSource(
	ctx context.Context,
	deploymentID string,
	imageDigest string,
	imageReference string,
	sourceRevision string,
	commitMessage string,
) error {
	if deploymentID == "" {
		return errors.New("deployment ID is required")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE deployments
SET image_digest = ?, image_reference = ?, source_revision = ?, source_commit_message = ?
WHERE id = ? AND status = 'running'`,
			imageDigest, imageReference, nullableString(sourceRevision), nullableString(commitMessage), deploymentID,
		)
		if err != nil {
			return fmt.Errorf("update deployment source: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deployment source update: %w", err)
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

func (store *Store) FinishDeployment(ctx context.Context, deploymentID, status, code, message string, finishedAtMillis int64) error {
	if deploymentID == "" || code == "" || finishedAtMillis <= 0 {
		return errors.New("finish deployment input is incomplete")
	}
	if status != "failed" && status != "interrupted" && status != "skipped" {
		return errors.New("deployment can only finish as failed, interrupted, or skipped")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE deployments
SET status = ?, error_code = ?, error_message = ?, finished_at = ?
WHERE id = ? AND status = 'running'`, status, code, message, finishedAtMillis, deploymentID)
		if err != nil {
			return fmt.Errorf("finish deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count finished deployment update: %w", err)
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

func (store *Store) LatestFailedDeployment(ctx context.Context, serviceID, configHash, imageDigest string) (bool, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM deployments
  WHERE service_id = ? AND service_config_hash = ? AND image_digest = ? AND status = 'failed'
  ORDER BY created_at DESC LIMIT 1
)`, serviceID, configHash, imageDigest).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check failed deployment pair: %w", err)
	}
	return exists == 1, nil
}

// LatestUploadedDeployment returns the newest deployment that already resolved an
// uploaded image. Used when desired state has no active deployment yet (first
// upload failed after import, or env changed before publish) so reconcile can
// override with current config instead of blocking on another push.
func (store *Store) LatestUploadedDeployment(ctx context.Context, serviceID string) (DeploymentRecord, error) {
	if serviceID == "" {
		return DeploymentRecord{}, errors.New("service ID is required")
	}
	deployment, err := scanDeploymentRecord(store.database.QueryRowContext(ctx, `
		SELECT id, service_id, image_digest, image_reference, image_revision_id, source_revision,
		       source_commit_message, service_config_hash, snapshot_json, status,
       error_code, error_message, created_at, finished_at
FROM deployments
WHERE service_id = ?
  AND IFNULL(image_revision_id, '') != ''
  AND IFNULL(image_reference, '') != ''
  AND IFNULL(image_digest, '') != ''
ORDER BY created_at DESC, id DESC
LIMIT 1`, serviceID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DeploymentRecord{}, ErrDeploymentNotFound
		}
		return DeploymentRecord{}, err
	}
	return deployment, nil
}

func (store *Store) Deployment(ctx context.Context, deploymentID string) (DeploymentRecord, error) {
	deployment, err := scanDeploymentRecord(store.database.QueryRowContext(ctx, `
		SELECT id, service_id, image_digest, image_reference, image_revision_id, source_revision,
		       source_commit_message, service_config_hash, snapshot_json, status,
       error_code, error_message, created_at, finished_at
FROM deployments WHERE id = ?`, deploymentID))
	if err != nil {
		return DeploymentRecord{}, err
	}
	return deployment, nil
}
