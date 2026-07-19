package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

var ErrPreviewDeploymentChanged = errors.New("PR preview deployment changed")

type PreviewDeployment struct {
	ID                  string
	ServiceID           string
	PullRequestNumber   int
	SourceRevision      string
	CommitMessage       string
	Hostname            string
	TargetPort          int
	ImageDigest         string
	ImageReference      string
	ConfigHash          string
	Snapshot            serviceconfig.Snapshot
	Status              string
	ErrorCode           string
	ErrorMessage        string
	GitHubDeploymentID  int64
	GitHubCommentID     int64
	CloudflareRecordIDs []string
	CreatedAtMillis     int64
	FinishedAtMillis    int64
	ExpiresAtMillis     int64
}

type BeginPreviewDeployment struct {
	ID                string
	ServiceID         string
	PullRequestNumber int
	SourceRevision    string
	Hostname          string
	TargetPort        int
	ConfigHash        string
	SnapshotJSON      []byte
	CreatedAtMillis   int64
	ExpiresAtMillis   int64
}

func (store *Store) BeginPreviewDeployment(ctx context.Context, input BeginPreviewDeployment) error {
	if input.ID == "" || input.ServiceID == "" || input.PullRequestNumber <= 0 || input.SourceRevision == "" ||
		input.Hostname == "" || input.TargetPort < 1 || input.TargetPort > 65535 || input.ConfigHash == "" ||
		len(input.SnapshotJSON) == 0 || input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis {
		return errors.New("begin PR preview deployment input is incomplete")
	}
	var snapshot serviceconfig.Snapshot
	if err := json.Unmarshal(input.SnapshotJSON, &snapshot); err != nil {
		return fmt.Errorf("decode PR preview snapshot: %w", err)
	}
	_, canonicalJSON, hash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		return err
	}
	if hash != input.ConfigHash || string(canonicalJSON) != string(input.SnapshotJSON) {
		return errors.New("PR preview snapshot is not canonical or does not match its hash")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO preview_deployments(
  id, service_id, pull_request_number, source_revision, hostname, target_port,
  image_digest, image_reference, service_config_hash, snapshot_json, status,
  created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, '', '', ?, ?, 'building', ?, ?)`,
			input.ID, input.ServiceID, input.PullRequestNumber, input.SourceRevision,
			input.Hostname, input.TargetPort, input.ConfigHash, string(input.SnapshotJSON),
			input.CreatedAtMillis, input.ExpiresAtMillis,
		)
		if err != nil {
			return fmt.Errorf("begin PR preview deployment: %w", err)
		}
		return nil
	})
}

func (store *Store) SetPreviewGitHubDeployment(ctx context.Context, previewID string, deploymentID int64) error {
	if previewID == "" || deploymentID <= 0 {
		return errors.New("PR preview GitHub deployment input is incomplete")
	}
	return store.updatePreviewBuilding(ctx, previewID, `github_deployment_id = ?`, deploymentID)
}

func (store *Store) SetPreviewGitHubComment(ctx context.Context, previewID string, commentID int64) error {
	if previewID == "" || commentID <= 0 {
		return errors.New("PR preview GitHub comment input is incomplete")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments SET github_comment_id = ? WHERE id = ?`, commentID, previewID)
		if err != nil {
			return fmt.Errorf("save PR preview GitHub comment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) SetPreviewBuild(ctx context.Context, previewID, imageDigest, imageReference, commitMessage string) error {
	if previewID == "" || imageDigest == "" || imageReference == "" {
		return errors.New("PR preview build result is incomplete")
	}
	return store.updatePreviewBuilding(ctx, previewID, `
image_digest = ?, image_reference = ?, source_commit_message = ?`,
		imageDigest, imageReference, nullableString(commitMessage))
}

func (store *Store) SetPreviewDNSRecords(ctx context.Context, previewID string, recordIDs []string) error {
	if previewID == "" || len(recordIDs) == 0 {
		return errors.New("PR preview DNS result is incomplete")
	}
	for _, recordID := range recordIDs {
		if recordID == "" {
			return errors.New("PR preview DNS record ID is empty")
		}
	}
	recordsJSON, err := json.Marshal(recordIDs)
	if err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments SET cloudflare_records_json = ?
WHERE id = ? AND status IN ('building', 'active')`, string(recordsJSON), previewID)
		if err != nil {
			return fmt.Errorf("save PR preview DNS records: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) updatePreviewBuilding(ctx context.Context, previewID, assignment string, values ...any) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		arguments := append(values, previewID)
		result, err := transaction.ExecContext(ctx, `UPDATE preview_deployments SET `+assignment+` WHERE id = ? AND status = 'building'`, arguments...)
		if err != nil {
			return fmt.Errorf("update building PR preview deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) ActivatePreviewDeployment(ctx context.Context, previewID, expectedActiveID string, recordIDs []string, finishedAtMillis int64) error {
	if previewID == "" || finishedAtMillis <= 0 {
		return errors.New("activate PR preview deployment input is incomplete")
	}
	recordsJSON, err := json.Marshal(recordIDs)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var serviceID string
		var pullRequest int
		var status string
		if err := transaction.QueryRowContext(ctx, `
SELECT service_id, pull_request_number, status FROM preview_deployments WHERE id = ?`, previewID).Scan(
			&serviceID, &pullRequest, &status,
		); err != nil {
			return fmt.Errorf("load PR preview for activation: %w", err)
		}
		if status != "building" {
			return ErrPreviewDeploymentChanged
		}
		var activeID sql.NullString
		err := transaction.QueryRowContext(ctx, `
SELECT id FROM preview_deployments
WHERE service_id = ? AND pull_request_number = ? AND status = 'active'`, serviceID, pullRequest).Scan(&activeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load active PR preview: %w", err)
		}
		if activeID.String != expectedActiveID {
			return ErrPreviewDeploymentChanged
		}
		if activeID.Valid {
			if _, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments SET status = 'stopped', finished_at = ? WHERE id = ? AND status = 'active'`,
				finishedAtMillis, activeID.String,
			); err != nil {
				return fmt.Errorf("supersede active PR preview: %w", err)
			}
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments
SET status = 'active', cloudflare_records_json = ?, finished_at = NULL
WHERE id = ? AND status = 'building'`, string(recordsJSON), previewID)
		if err != nil {
			return fmt.Errorf("activate PR preview deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) FinishPreviewDeployment(ctx context.Context, previewID, status, code, message string, finishedAtMillis int64) error {
	if previewID == "" || finishedAtMillis <= 0 || (status != "failed" && status != "skipped" && status != "interrupted") {
		return errors.New("finish PR preview deployment input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments
SET status = ?, error_code = ?, error_message = ?, finished_at = ?
WHERE id = ? AND status = 'building'`, status, nullableString(code), nullableString(message), finishedAtMillis, previewID)
		if err != nil {
			return fmt.Errorf("finish PR preview deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) StopPreviewDeployment(ctx context.Context, previewID string, finishedAtMillis int64) error {
	if previewID == "" || finishedAtMillis <= 0 {
		return errors.New("stop PR preview deployment input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments SET status = 'stopped', finished_at = ?
WHERE id = ? AND status = 'active'`, finishedAtMillis, previewID)
		if err != nil {
			return fmt.Errorf("stop PR preview deployment: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

func (store *Store) ActivePreviewDeployment(ctx context.Context, serviceID string, pullRequestNumber int) (PreviewDeployment, error) {
	return scanPreviewDeployment(store.database.QueryRowContext(ctx, previewSelect+`
WHERE service_id = ? AND pull_request_number = ? AND status = 'active'`, serviceID, pullRequestNumber))
}

func (store *Store) ActivePreviewDeployments(ctx context.Context) ([]PreviewDeployment, error) {
	return store.previewDeployments(ctx, previewSelect+` WHERE status = 'active' ORDER BY created_at, id`)
}

func (store *Store) PreviewDeploymentsForService(ctx context.Context, projectID, serviceID string) ([]PreviewDeployment, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	return store.previewDeployments(ctx, previewSelect+` WHERE service_id = ? ORDER BY created_at DESC, id DESC`, serviceID)
}

func (store *Store) PreviewDeployment(ctx context.Context, projectID, serviceID, previewID string) (PreviewDeployment, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return PreviewDeployment{}, err
	}
	preview, err := scanPreviewDeployment(store.database.QueryRowContext(ctx, previewSelect+`
WHERE id = ? AND service_id = ?`, previewID, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return PreviewDeployment{}, ErrDeploymentNotFound
	}
	return preview, err
}

func (store *Store) LatestPreviewCommentID(ctx context.Context, serviceID string, pullRequestNumber int) (int64, error) {
	var commentID sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT github_comment_id FROM preview_deployments
WHERE service_id = ? AND pull_request_number = ? AND github_comment_id IS NOT NULL
ORDER BY created_at DESC LIMIT 1`, serviceID, pullRequestNumber).Scan(&commentID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load latest PR preview comment: %w", err)
	}
	return commentID.Int64, nil
}

func (store *Store) ExpiredActivePreviewDeployments(ctx context.Context, beforeMillis int64) ([]PreviewDeployment, error) {
	return store.previewDeployments(ctx, previewSelect+` WHERE status = 'active' AND expires_at <= ? ORDER BY expires_at, id`, beforeMillis)
}

func (store *Store) DeleteFinishedPreviewDeployments(ctx context.Context, beforeMillis int64) ([]PreviewDeployment, error) {
	previews, err := store.previewDeployments(ctx, previewSelect+`
WHERE status IN ('failed', 'skipped', 'stopped', 'interrupted')
  AND finished_at < ?
  AND cloudflare_records_json = '[]'
ORDER BY finished_at, id`, beforeMillis)
	if err != nil || len(previews) == 0 {
		return previews, err
	}
	return previews, store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
DELETE FROM preview_deployments
WHERE status IN ('failed', 'skipped', 'stopped', 'interrupted')
  AND finished_at < ?
  AND cloudflare_records_json = '[]'`, beforeMillis)
		return err
	})
}

func (store *Store) FinishedPreviewDeploymentsWithDNS(ctx context.Context) ([]PreviewDeployment, error) {
	return store.previewDeployments(ctx, previewSelect+`
WHERE status IN ('failed', 'skipped', 'stopped', 'interrupted')
  AND cloudflare_records_json != '[]'
ORDER BY finished_at, id`)
}

func (store *Store) ClearPreviewDNSRecords(ctx context.Context, previewID string) error {
	if previewID == "" {
		return errors.New("PR preview ID is required")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE preview_deployments SET cloudflare_records_json = '[]' WHERE id = ?`, previewID)
		if err != nil {
			return fmt.Errorf("clear PR preview DNS records: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrPreviewDeploymentChanged
		}
		return nil
	})
}

const previewSelect = `
SELECT id, service_id, pull_request_number, source_revision, source_commit_message,
       hostname, target_port, image_digest, image_reference, service_config_hash,
       snapshot_json, status, error_code, error_message, github_deployment_id,
       github_comment_id, cloudflare_records_json, created_at, finished_at, expires_at
FROM preview_deployments`

func (store *Store) previewDeployments(ctx context.Context, query string, arguments ...any) ([]PreviewDeployment, error) {
	rows, err := store.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list PR preview deployments: %w", err)
	}
	defer rows.Close()
	result := make([]PreviewDeployment, 0)
	for rows.Next() {
		preview, err := scanPreviewDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, preview)
	}
	return result, rows.Err()
}

type previewScanner interface {
	Scan(...any) error
}

func scanPreviewDeployment(scanner previewScanner) (PreviewDeployment, error) {
	var preview PreviewDeployment
	var commitMessage, errorCode, errorMessage sql.NullString
	var githubDeploymentID, githubCommentID, finishedAt sql.NullInt64
	var snapshotJSON, cloudflareRecordsJSON string
	if err := scanner.Scan(
		&preview.ID, &preview.ServiceID, &preview.PullRequestNumber, &preview.SourceRevision, &commitMessage,
		&preview.Hostname, &preview.TargetPort, &preview.ImageDigest, &preview.ImageReference, &preview.ConfigHash,
		&snapshotJSON, &preview.Status, &errorCode, &errorMessage, &githubDeploymentID,
		&githubCommentID, &cloudflareRecordsJSON, &preview.CreatedAtMillis, &finishedAt, &preview.ExpiresAtMillis,
	); err != nil {
		return PreviewDeployment{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &preview.Snapshot); err != nil {
		return PreviewDeployment{}, fmt.Errorf("decode PR preview snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(cloudflareRecordsJSON), &preview.CloudflareRecordIDs); err != nil {
		return PreviewDeployment{}, fmt.Errorf("decode PR preview DNS records: %w", err)
	}
	preview.CommitMessage = commitMessage.String
	preview.ErrorCode = errorCode.String
	preview.ErrorMessage = errorMessage.String
	preview.GitHubDeploymentID = githubDeploymentID.Int64
	preview.GitHubCommentID = githubCommentID.Int64
	preview.FinishedAtMillis = finishedAt.Int64
	return preview, nil
}
