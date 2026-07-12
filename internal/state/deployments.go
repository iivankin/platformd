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
	ConfigHash       string
	Snapshot         serviceconfig.Snapshot
	Status           string
	ErrorCode        string
	ErrorMessage     string
	CreatedAtMillis  int64
	FinishedAtMillis int64
}

type BeginDeployment struct {
	ID              string
	ServiceID       string
	ImageDigest     string
	ConfigHash      string
	SnapshotJSON    []byte
	CreatedAtMillis int64
}

func (store *Store) BeginDeployment(ctx context.Context, input BeginDeployment) error {
	if input.ID == "" || input.ServiceID == "" || input.ImageDigest == "" || input.ConfigHash == "" || len(input.SnapshotJSON) == 0 || input.CreatedAtMillis <= 0 {
		return errors.New("begin deployment input is incomplete")
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
  id, service_id, image_digest, service_config_hash, snapshot_json, status, created_at
) VALUES (?, ?, ?, ?, ?, 'running', ?)`,
			input.ID, input.ServiceID, input.ImageDigest, input.ConfigHash, string(input.SnapshotJSON), input.CreatedAtMillis,
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
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var activeDeploymentID sql.NullString
		var status string
		err := transaction.QueryRowContext(ctx, `
SELECT s.active_deployment_id, d.status
FROM services s JOIN deployments d ON d.id = ? AND d.service_id = s.id
WHERE s.id = ? AND s.enabled = 1`, deploymentID, serviceID).Scan(&activeDeploymentID, &status)
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

func (store *Store) FailDeployment(ctx context.Context, deploymentID, code, message string, finishedAtMillis int64) error {
	if deploymentID == "" || code == "" || finishedAtMillis <= 0 {
		return errors.New("fail deployment input is incomplete")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE deployments
SET status = 'failed', error_code = ?, error_message = ?, finished_at = ?
WHERE id = ? AND status = 'running'`, code, message, finishedAtMillis, deploymentID)
		if err != nil {
			return fmt.Errorf("fail deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count failed deployment update: %w", err)
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

func (store *Store) Deployment(ctx context.Context, deploymentID string) (DeploymentRecord, error) {
	deployment, err := scanDeploymentRecord(store.database.QueryRowContext(ctx, `
SELECT id, service_id, image_digest, service_config_hash, snapshot_json, status,
       error_code, error_message, created_at, finished_at
FROM deployments WHERE id = ?`, deploymentID))
	if err != nil {
		return DeploymentRecord{}, err
	}
	return deployment, nil
}
