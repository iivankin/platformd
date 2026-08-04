package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ImageCleanupMode string

const (
	ImageCleanupStandard  ImageCleanupMode = "standard"
	ImageCleanupCritical  ImageCleanupMode = "critical"
	ImageCleanupEmergency ImageCleanupMode = "emergency"
)

type ImageCleanupFiles struct {
	UploadPaths  []string
	ArchivePaths []string
}

func (store *Store) ImageStorageFiles(ctx context.Context) (ImageCleanupFiles, error) {
	result := ImageCleanupFiles{}
	queries := []struct {
		target *[]string
		query  string
	}{
		{target: &result.UploadPaths, query: "SELECT temporary_path FROM service_image_uploads ORDER BY temporary_path"},
		{target: &result.ArchivePaths, query: "SELECT archive_path FROM service_image_revisions ORDER BY archive_path"},
	}
	for _, item := range queries {
		rows, err := store.database.QueryContext(ctx, item.query)
		if err != nil {
			return ImageCleanupFiles{}, err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return ImageCleanupFiles{}, err
			}
			*item.target = append(*item.target, path)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return ImageCleanupFiles{}, err
		}
	}
	return result, nil
}

func (store *Store) ActiveImageUploads(ctx context.Context, expiresAtOrBefore int64, all bool) ([]ImageUpload, error) {
	condition := "expires_at <= ?"
	arguments := []any{expiresAtOrBefore}
	if all {
		condition = "1 = 1"
		arguments = nil
	} else if expiresAtOrBefore <= 0 {
		return nil, errors.New("image upload expiry is invalid")
	}
	rows, err := store.database.QueryContext(ctx, imageUploadSelect+`
 WHERE status IN ('uploading', 'importing', 'deploying') AND `+condition+` ORDER BY created_at, id`, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []ImageUpload
	for rows.Next() {
		upload, err := scanImageUpload(rows)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

// CancelImageUploads makes the selected in-flight uploads terminal before
// their files are removed. Callers cancel and wait for local processors first.
func (store *Store) CancelImageUploads(ctx context.Context, expiresAtOrBefore int64, all bool, nowMillis int64) ([]string, error) {
	if expiresAtOrBefore <= 0 || nowMillis <= 0 {
		return nil, errors.New("image upload cancellation time is invalid")
	}
	var paths []string
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		condition := "expires_at <= ?"
		arguments := []any{expiresAtOrBefore}
		if all {
			condition = "1 = 1"
			arguments = nil
		}
		rows, err := transaction.QueryContext(ctx, `
SELECT temporary_path FROM service_image_uploads
WHERE status IN ('uploading', 'importing', 'deploying') AND `+condition, arguments...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return err
			}
			paths = append(paths, path)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		updateArguments := []any{nowMillis, nowMillis}
		updateArguments = append(updateArguments, arguments...)
		_, err = transaction.ExecContext(ctx, `
UPDATE service_image_uploads
SET status = 'failed', error_code = 'upload_cancelled', error_message = 'Upload cancelled by image cleanup',
    updated_at = ?, expires_at = ?
WHERE status IN ('uploading', 'importing', 'deploying') AND `+condition, updateArguments...)
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
UPDATE service_image_revisions
SET status = CASE WHEN IFNULL(image_digest, '') != '' THEN 'retired' ELSE 'failed' END, retired_at = ?
WHERE status = 'importing' AND id IN (
  SELECT image_revision_id FROM service_image_uploads WHERE status = 'failed' AND error_code = 'upload_cancelled'
)`, nowMillis)
		return err
	})
	return paths, err
}

// DeleteImageCleanupCandidates removes only state that cannot be restarted or
// backed up. The caller removes the returned files after the transaction.
func (store *Store) DeleteImageCleanupCandidates(
	ctx context.Context,
	mode ImageCleanupMode,
	nowMillis int64,
	productionRetiredBeforeMillis int64,
) (ImageCleanupFiles, error) {
	if nowMillis <= 0 || productionRetiredBeforeMillis <= 0 {
		return ImageCleanupFiles{}, errors.New("image cleanup time is invalid")
	}
	if mode != ImageCleanupStandard && mode != ImageCleanupCritical && mode != ImageCleanupEmergency {
		return ImageCleanupFiles{}, errors.New("image cleanup mode is invalid")
	}
	result := ImageCleanupFiles{}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		rows, err := transaction.QueryContext(ctx, `
SELECT temporary_path FROM service_image_uploads
WHERE status IN ('succeeded', 'failed', 'superseded') AND expires_at <= ?`, nowMillis)
		if err != nil {
			return err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return err
			}
			result.UploadPaths = append(result.UploadPaths, path)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
DELETE FROM service_image_uploads
WHERE status IN ('succeeded', 'failed', 'superseded') AND expires_at <= ?`, nowMillis); err != nil {
			return err
		}

		condition, arguments := imageRevisionCleanupCondition(mode, nowMillis, productionRetiredBeforeMillis)
		rows, err = transaction.QueryContext(ctx, `
SELECT id, archive_path FROM service_image_revisions r
WHERE (`+condition+`) AND NOT EXISTS(
  SELECT 1 FROM backups b WHERE b.resource_kind = 'image' AND b.resource_id = r.id AND b.status = 'running'
)`, arguments...)
		if err != nil {
			return err
		}
		type candidate struct{ id, path string }
		var candidates []candidate
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.path); err != nil {
				_ = rows.Close()
				return err
			}
			candidates = append(candidates, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, item := range candidates {
			if _, err := transaction.ExecContext(ctx, `
DELETE FROM deployments
WHERE image_revision_id = ? AND id NOT IN(
  SELECT active_deployment_id FROM services WHERE active_deployment_id IS NOT NULL
)`, item.id); err != nil {
				return err
			}
			deleted, err := transaction.ExecContext(ctx, `
DELETE FROM service_image_revisions
WHERE id = ?
  AND status != 'importing'
  AND NOT EXISTS(SELECT 1 FROM services WHERE active_deployment_id = service_image_revisions.deployment_id)
  AND NOT EXISTS(SELECT 1 FROM preview_deployments WHERE image_revision_id = service_image_revisions.id AND status = 'active')
  AND NOT EXISTS(SELECT 1 FROM backups WHERE resource_kind = 'image' AND resource_id = service_image_revisions.id AND status = 'running')`, item.id)
			if err != nil {
				return err
			}
			count, err := deleted.RowsAffected()
			if err != nil {
				return err
			}
			if count == 1 {
				result.ArchivePaths = append(result.ArchivePaths, item.path)
			}
		}
		return nil
	})
	if err != nil {
		return ImageCleanupFiles{}, fmt.Errorf("delete image cleanup candidates: %w", err)
	}
	return result, nil
}

func imageRevisionCleanupCondition(mode ImageCleanupMode, nowMillis, productionRetiredBeforeMillis int64) (string, []any) {
	standard := `
r.status = 'failed'
OR (r.kind = 'preview' AND r.status IN ('active', 'retired') AND r.expires_at <= ?
    AND NOT EXISTS(SELECT 1 FROM preview_deployments p WHERE p.image_revision_id = r.id AND p.status = 'active'))
OR (r.kind = 'production' AND r.status = 'retired' AND r.retired_at <= ?)`
	if mode == ImageCleanupStandard {
		return standard, []any{nowMillis, productionRetiredBeforeMillis}
	}
	production := `r.kind = 'production' AND r.status != 'active'`
	if mode == ImageCleanupCritical {
		production += ` AND r.id NOT IN (
  SELECT id FROM (
    SELECT id, row_number() OVER (PARTITION BY service_id ORDER BY COALESCE(retired_at, created_at) DESC, id DESC) AS position
    FROM service_image_revisions WHERE kind = 'production' AND status = 'retired'
  ) WHERE position = 1
)`
	}
	return `r.status = 'failed'
OR (r.kind = 'preview' AND r.status != 'importing'
    AND NOT EXISTS(SELECT 1 FROM preview_deployments p WHERE p.image_revision_id = r.id AND p.status = 'active'))
OR (` + production + `)`, nil
}
