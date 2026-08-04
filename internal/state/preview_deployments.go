package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

type PreviewDeployment struct {
	ID                  string
	ServiceID           string
	Tag                 string
	ImageRevisionID     string
	Hostname            string
	TargetPort          int
	ImageDigest         string
	ImageReference      string
	ConfigHash          string
	Snapshot            serviceconfig.Snapshot
	Status              string
	ErrorCode           string
	ErrorMessage        string
	CloudflareRecordIDs []string
	CreatedAtMillis     int64
	UpdatedAtMillis     int64
	FinishedAtMillis    int64
	ExpiresAtMillis     int64
}

type BeginPreviewDeployment struct {
	ID              string
	ServiceID       string
	Tag             string
	ImageRevisionID string
	Hostname        string
	TargetPort      int
	ImageDigest     string
	ImageReference  string
	ConfigHash      string
	SnapshotJSON    []byte
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

func (store *Store) BeginPreviewDeployment(ctx context.Context, input BeginPreviewDeployment) error {
	if input.ID == "" || input.ServiceID == "" || input.Tag == "" || input.ImageRevisionID == "" ||
		input.Hostname == "" || input.TargetPort < 1 || input.TargetPort > 65535 || input.ImageDigest == "" ||
		input.ImageReference == "" || input.ConfigHash == "" || len(input.SnapshotJSON) == 0 ||
		input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis {
		return errors.New("begin preview deployment input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO preview_deployments(
 id, service_id, tag, image_revision_id, hostname, target_port, image_digest,
 image_reference, service_config_hash, snapshot_json, status, created_at, updated_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'deploying', ?, ?, ?)`,
			input.ID, input.ServiceID, input.Tag, input.ImageRevisionID, input.Hostname,
			input.TargetPort, input.ImageDigest, input.ImageReference, input.ConfigHash,
			string(input.SnapshotJSON), input.CreatedAtMillis, input.CreatedAtMillis, input.ExpiresAtMillis,
		)
		return err
	})
}

func (store *Store) SetPreviewDNSRecords(ctx context.Context, previewID string, recordIDs []string) error {
	encoded, err := json.Marshal(recordIDs)
	if err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET cloudflare_records_json = ? WHERE id = ? AND status IN ('deploying', 'active')`, string(encoded), previewID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

func (store *Store) ActivatePreviewDeployment(ctx context.Context, previewID, expectedActiveID string, recordIDs []string, activatedAtMillis int64) error {
	encoded, err := json.Marshal(recordIDs)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var serviceID, tag, imageRevisionID string
		if err := transaction.QueryRowContext(ctx, `SELECT service_id, tag, image_revision_id FROM preview_deployments WHERE id = ? AND status = 'deploying'`, previewID).Scan(&serviceID, &tag, &imageRevisionID); err != nil {
			return ErrServiceChanged
		}
		var current sql.NullString
		if err := transaction.QueryRowContext(ctx, `SELECT id FROM preview_deployments WHERE service_id = ? AND tag = ? AND status = 'active'`, serviceID, tag).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current.String != expectedActiveID {
			return ErrServiceChanged
		}
		if current.Valid {
			if _, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET status = 'stopped', updated_at = ?, finished_at = ? WHERE id = ? AND status = 'active'`, activatedAtMillis, activatedAtMillis, current.String); err != nil {
				return err
			}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET status = 'retired', retired_at = ?
WHERE service_id = ? AND tag = ? AND kind = 'preview' AND status = 'active' AND id != ?`,
			activatedAtMillis, serviceID, tag, imageRevisionID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET status = 'active', cloudflare_records_json = ?, updated_at = ?, expires_at = ? WHERE id = ? AND status = 'deploying'`, string(encoded), activatedAtMillis, activatedAtMillis+PreviewRetentionMillis, previewID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrServiceChanged
		}
		result, err = transaction.ExecContext(ctx, `
UPDATE service_image_revisions
SET status = 'active', preview_id = ?, activated_at = ?, retired_at = NULL, expires_at = ?
WHERE id = ? AND service_id = ? AND tag = ? AND kind = 'preview' AND status IN ('importing', 'active', 'retired')`,
			previewID, activatedAtMillis, activatedAtMillis+PreviewRetentionMillis,
			imageRevisionID, serviceID, tag)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

const PreviewRetentionMillis = int64(14 * 24 * 60 * 60 * 1000)

func (store *Store) FinishPreviewDeployment(ctx context.Context, previewID, status, code, message string, finishedAtMillis int64) error {
	if status != "failed" && status != "interrupted" {
		return errors.New("invalid preview terminal status")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET status = ?, error_code = ?, error_message = ?, updated_at = ?, finished_at = ? WHERE id = ? AND status = 'deploying'`, status, code, message, finishedAtMillis, finishedAtMillis, previewID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrServiceChanged
		}
		return nil
	})
}

func (store *Store) StopPreviewDeployment(ctx context.Context, previewID string, stoppedAtMillis int64) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET status = 'stopped', updated_at = ?, finished_at = ? WHERE id = ? AND status = 'active'`, stoppedAtMillis, stoppedAtMillis, previewID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed == 0 {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
UPDATE service_image_revisions SET status = 'retired', retired_at = ?
WHERE id = (SELECT image_revision_id FROM preview_deployments WHERE id = ?) AND status = 'active'`, stoppedAtMillis, previewID)
		return err
	})
}

func (store *Store) ActivePreviewDeployment(ctx context.Context, serviceID, tag string) (PreviewDeployment, error) {
	return scanPreview(store.database.QueryRowContext(ctx, previewSelect+` WHERE service_id = ? AND tag = ? AND status = 'active'`, serviceID, tag))
}

func (store *Store) ActivePreviewDeployments(ctx context.Context) ([]PreviewDeployment, error) {
	return store.previewList(ctx, previewSelect+` WHERE status = 'active' ORDER BY created_at, id`)
}

func (store *Store) ExpiredActivePreviewDeployments(ctx context.Context, nowMillis int64) ([]PreviewDeployment, error) {
	return store.previewList(ctx, previewSelect+` WHERE status = 'active' AND expires_at <= ? ORDER BY expires_at, id`, nowMillis)
}

func (store *Store) FinishedPreviewDeploymentsWithDNS(ctx context.Context) ([]PreviewDeployment, error) {
	return store.previewList(ctx, previewSelect+` WHERE status IN ('failed', 'stopped', 'interrupted') AND cloudflare_records_json != '[]' ORDER BY created_at, id`)
}

func (store *Store) ClearPreviewDNSRecords(ctx context.Context, previewID string) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET cloudflare_records_json = '[]' WHERE id = ?`, previewID)
		return err
	})
}

func (store *Store) DeleteFinishedPreviewDeployments(ctx context.Context, beforeMillis int64) ([]PreviewDeployment, error) {
	items, err := store.previewList(ctx, previewSelect+` WHERE status IN ('failed', 'stopped', 'interrupted') AND finished_at < ? AND cloudflare_records_json = '[]' ORDER BY finished_at, id`, beforeMillis)
	if err != nil {
		return nil, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `DELETE FROM preview_deployments WHERE status IN ('failed', 'stopped', 'interrupted') AND finished_at < ? AND cloudflare_records_json = '[]'`, beforeMillis)
		return err
	})
	return items, err
}

func (store *Store) PreviewDeployment(ctx context.Context, projectID, serviceID, previewID string) (PreviewDeployment, error) {
	preview, err := scanPreview(store.database.QueryRowContext(ctx, previewSelect+` WHERE id = ? AND service_id = ? AND service_id IN (SELECT id FROM services WHERE project_id = ?)`, previewID, serviceID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewDeployment{}, ErrDeploymentNotFound
	}
	return preview, err
}

func (store *Store) PreviewDeploymentsForService(ctx context.Context, projectID, serviceID string) ([]PreviewDeployment, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	return store.previewList(ctx, previewSelect+` WHERE service_id = ? ORDER BY created_at DESC, id DESC`, serviceID)
}

const previewSelect = `SELECT id, service_id, tag, image_revision_id, hostname, target_port,
image_digest, image_reference, service_config_hash, snapshot_json, status, error_code,
error_message, cloudflare_records_json, created_at, updated_at, finished_at, expires_at
FROM preview_deployments`

func (store *Store) previewList(ctx context.Context, query string, args ...any) ([]PreviewDeployment, error) {
	rows, err := store.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PreviewDeployment
	for rows.Next() {
		item, err := scanPreview(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanPreview(scanner rowScanner) (PreviewDeployment, error) {
	var item PreviewDeployment
	var snapshotJSON, recordJSON string
	var code, message sql.NullString
	var finished sql.NullInt64
	if err := scanner.Scan(&item.ID, &item.ServiceID, &item.Tag, &item.ImageRevisionID,
		&item.Hostname, &item.TargetPort, &item.ImageDigest, &item.ImageReference, &item.ConfigHash,
		&snapshotJSON, &item.Status, &code, &message, &recordJSON, &item.CreatedAtMillis,
		&item.UpdatedAtMillis, &finished, &item.ExpiresAtMillis); err != nil {
		return PreviewDeployment{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &item.Snapshot); err != nil {
		return PreviewDeployment{}, fmt.Errorf("decode preview snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(recordJSON), &item.CloudflareRecordIDs); err != nil {
		return PreviewDeployment{}, fmt.Errorf("decode preview DNS records: %w", err)
	}
	item.ErrorCode, item.ErrorMessage, item.FinishedAtMillis = code.String, message.String, finished.Int64
	return item, nil
}
