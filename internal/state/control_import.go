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
		target.Endpoint == "" || target.Region == "" || target.Bucket == "" || target.AccessKeyID == "" ||
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
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation
SET access_team_domain = ?, access_audience = ?, recovery_mode = 1, updated_at = ?
WHERE singleton = 1`, teamDomain, audience, input.ImportedAtMillis); err != nil {
			return fmt.Errorf("enter recovery mode: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO backup_target(
  singleton, endpoint, region, bucket, prefix, access_key_id,
  secret_access_key_encrypted, created_at, updated_at
) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
  endpoint = excluded.endpoint,
  region = excluded.region,
  bucket = excluded.bucket,
  prefix = excluded.prefix,
  access_key_id = excluded.access_key_id,
  secret_access_key_encrypted = excluded.secret_access_key_encrypted,
  created_at = excluded.created_at,
  updated_at = excluded.updated_at`,
			target.Endpoint, target.Region, target.Bucket, target.Prefix, target.AccessKeyID,
			target.SecretAccessKeyEncrypted, input.ImportedAtMillis, input.ImportedAtMillis,
		); err != nil {
			return fmt.Errorf("replace restored backup target: %w", err)
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
