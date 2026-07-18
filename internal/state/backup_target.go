package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrBackupTargetNotFound = errors.New("backup target not found")
	ErrBackupTargetInUse    = errors.New("backup target is in use")
)

type BackupTarget struct {
	ID                       string
	Name                     string
	Endpoint                 string
	Region                   string
	Bucket                   string
	Prefix                   string
	AccessKeyID              string
	SecretAccessKeyEncrypted []byte
	CreatedAtMillis          int64
	UpdatedAtMillis          int64
}

type SetBackupTarget struct {
	Target               BackupTarget
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) BackupTargets(ctx context.Context) ([]BackupTarget, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, endpoint, region, bucket, prefix, access_key_id,
       secret_access_key_encrypted, created_at, updated_at
FROM backup_targets ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BackupTarget, 0)
	for rows.Next() {
		target, err := scanBackupTarget(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (store *Store) BackupTarget(ctx context.Context, targetID string) (BackupTarget, error) {
	if targetID == "" {
		return BackupTarget{}, ErrBackupTargetNotFound
	}
	target, err := scanBackupTarget(store.database.QueryRowContext(ctx, `
SELECT id, name, endpoint, region, bucket, prefix, access_key_id,
       secret_access_key_encrypted, created_at, updated_at
FROM backup_targets WHERE id = ?`, targetID))
	if errors.Is(err, sql.ErrNoRows) {
		return BackupTarget{}, ErrBackupTargetNotFound
	}
	return target, err
}

func (store *Store) ControlBackupTargetID(ctx context.Context) (string, error) {
	var targetID sql.NullString
	if err := store.database.QueryRowContext(ctx, `
SELECT backup_control_target_id FROM installation WHERE singleton = 1`).Scan(&targetID); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotInitialized
	} else if err != nil {
		return "", err
	}
	return targetID.String, nil
}

func (store *Store) SetBackupTarget(ctx context.Context, input SetBackupTarget) (BackupTarget, error) {
	target := input.Target
	if target.ID == "" || target.Name == "" || target.Endpoint == "" || target.Region == "" ||
		target.Bucket == "" || target.AccessKeyID == "" || len(target.SecretAccessKeyEncrypted) == 0 ||
		input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return BackupTarget{}, errors.New("set backup target input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return BackupTarget{}, err
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var createdAt int64
		err := transaction.QueryRowContext(ctx, `
SELECT created_at FROM backup_targets WHERE id = ?`, target.ID).Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO backup_targets(
  id, name, endpoint, region, bucket, prefix, access_key_id,
  secret_access_key_encrypted, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  endpoint = excluded.endpoint,
  region = excluded.region,
  bucket = excluded.bucket,
  prefix = excluded.prefix,
  access_key_id = excluded.access_key_id,
  secret_access_key_encrypted = excluded.secret_access_key_encrypted,
  updated_at = excluded.updated_at`,
			target.ID, target.Name, target.Endpoint, target.Region, target.Bucket, target.Prefix,
			target.AccessKeyID, target.SecretAccessKeyEncrypted, createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("set backup target: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "name": target.Name, "endpoint": target.Endpoint,
			"region": target.Region, "bucket": target.Bucket, "prefix": target.Prefix,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'backup_target.set', 'backup_target', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, target.ID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	if err != nil {
		return BackupTarget{}, err
	}
	return store.BackupTarget(ctx, target.ID)
}

type SetControlBackupTarget struct {
	TargetID             string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetControlBackupTarget(ctx context.Context, input SetControlBackupTarget) error {
	if input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("set control backup target input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if input.TargetID != "" {
			var exists int
			if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM backup_targets WHERE id = ?)`, input.TargetID).Scan(&exists); err != nil {
				return err
			}
			if exists != 1 {
				return ErrBackupTargetNotFound
			}
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE installation SET backup_control_target_id = ?, updated_at = ? WHERE singleton = 1`,
			nullableString(input.TargetID), input.UpdatedAtMillis)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrNotInitialized
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "targetId": input.TargetID,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'backup.control_target.set', 'backup_target', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.TargetID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
}

type DeleteBackupTarget struct {
	TargetID             string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) DeleteBackupTarget(ctx context.Context, input DeleteBackupTarget) error {
	if input.TargetID == "" || input.AuditEventID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete backup target input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var uses int
		if err := transaction.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM installation WHERE backup_control_target_id = ?) +
  (SELECT count(*) FROM registry_repositories WHERE backup_target_id = ?) +
  (SELECT count(*) FROM object_stores WHERE backup_target_id = ?) +
  (SELECT count(*) FROM managed_postgres WHERE backup_target_id = ?) +
  (SELECT count(*) FROM managed_redis WHERE backup_target_id = ?) +
  (SELECT count(*) FROM volumes WHERE backup_target_id = ?)`,
			input.TargetID, input.TargetID, input.TargetID, input.TargetID, input.TargetID, input.TargetID,
		).Scan(&uses); err != nil {
			return err
		}
		if uses != 0 {
			return ErrBackupTargetInUse
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM backup_targets WHERE id = ?", input.TargetID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return ErrBackupTargetNotFound
		}
		metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'backup_target.delete', 'backup_target', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.TargetID,
			nullableString(input.RequestCorrelationID), string(metadata), input.DeletedAtMillis)
		return err
	})
}

type backupTargetScanner interface{ Scan(...any) error }

func scanBackupTarget(scanner backupTargetScanner) (BackupTarget, error) {
	var target BackupTarget
	err := scanner.Scan(
		&target.ID, &target.Name, &target.Endpoint, &target.Region, &target.Bucket, &target.Prefix,
		&target.AccessKeyID, &target.SecretAccessKeyEncrypted, &target.CreatedAtMillis, &target.UpdatedAtMillis,
	)
	return target, err
}

func (store *Store) EmbeddedObjectStoreHostnameExists(ctx context.Context, hostname string) (bool, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM object_stores WHERE public_hostname = ?)`, hostname).Scan(&exists)
	return exists == 1, err
}
