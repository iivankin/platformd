package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type ControlImport struct {
	ExpectedInstallationID string
	AccessTeamDomain       *string
	AccessAudience         *string
	Target                 BackupTarget
	AuditEventID           string
	ImportedAtMillis       int64
}

// ImportControl applies the only local mutations allowed during disaster
// bootstrap in one transaction on the restored image.
func (store *Store) ImportControl(ctx context.Context, input ControlImport) error {
	target := input.Target
	if input.ExpectedInstallationID == "" || input.AuditEventID == "" || input.ImportedAtMillis <= 0 ||
		(input.AccessTeamDomain == nil) != (input.AccessAudience == nil) ||
		target.ID == "" || target.Endpoint == "" || target.Region == "" || target.Bucket == "" || target.AccessKeyID == "" ||
		len(target.SecretAccessKeyEncrypted) == 0 {
		return errors.New("control import input is incomplete")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var installationID, teamDomain, audience string
		if err := transaction.QueryRowContext(ctx, `
SELECT id, access_team_domain, access_audience
FROM installation WHERE singleton = 1`).Scan(&installationID, &teamDomain, &audience); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		if installationID != input.ExpectedInstallationID {
			return errors.New("restored installation ID differs from control manifest")
		}
		if input.AccessTeamDomain != nil {
			teamDomain = *input.AccessTeamDomain
			audience = *input.AccessAudience
		}
		if teamDomain == "" || audience == "" {
			return errors.New("restored Access configuration is empty")
		}
		// The control snapshot contains complete SQLite configuration, but a
		// fresh VPS has none of the corresponding Registry/S3 payload files.
		// Clear only content metadata at the one-time recovery boundary; each
		// resource restore will republish its own latest generation. Keeping
		// repositories, stores, credentials and policies makes recovery usable
		// without introducing a second control-state format.
		for _, statement := range []string{
			"DELETE FROM registry_uploads",
			"DELETE FROM registry_tags",
			"DELETE FROM registry_manifests",
			"DELETE FROM multipart_uploads",
			"DELETE FROM objects",
			"DELETE FROM object_payloads",
			// Resource generations are restored after the control database. Clear
			// first-mount markers so restored volumes are marked by their resource
			// restore, while a volume without a generation receives ordinary image
			// copy-up on its first post-recovery deployment.
			"DELETE FROM volume_initializations",
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("clear resource content for recovery: %w", err)
			}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation
SET access_team_domain = ?, access_audience = ?, backup_control_target_id = ?, recovery_mode = 1, updated_at = ?
WHERE singleton = 1`, teamDomain, audience, target.ID, input.ImportedAtMillis); err != nil {
			return fmt.Errorf("enter recovery mode: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE backup_targets
SET access_key_id = ?, secret_access_key_encrypted = ?, updated_at = ?
WHERE id = ? AND endpoint = ? AND region = ? AND bucket = ? AND prefix = ?`,
			target.AccessKeyID, target.SecretAccessKeyEncrypted, input.ImportedAtMillis, target.ID,
			target.Endpoint, target.Region, target.Bucket, target.Prefix,
		)
		if err != nil {
			return fmt.Errorf("refresh restored backup target credentials: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return errors.New("recovery storage is absent from the restored control snapshot")
		}
		metadata, err := json.Marshal(map[string]string{
			"endpoint": target.Endpoint, "region": target.Region,
			"bucket": target.Bucket, "prefix": target.Prefix,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'local_root', 'disaster_restore', 'control.restore', 'installation', ?, 'succeeded', ?, ?)`,
			input.AuditEventID, installationID, string(metadata), input.ImportedAtMillis,
		)
		return err
	})
}
