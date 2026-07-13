package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrBackupTargetNotFound = errors.New("backup target not found")

type BackupTarget struct {
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

func (store *Store) BackupTarget(ctx context.Context) (BackupTarget, error) {
	var target BackupTarget
	err := store.database.QueryRowContext(ctx, `
SELECT endpoint, region, bucket, prefix, access_key_id,
       secret_access_key_encrypted, created_at, updated_at
FROM backup_target WHERE singleton = 1`).Scan(
		&target.Endpoint, &target.Region, &target.Bucket, &target.Prefix,
		&target.AccessKeyID, &target.SecretAccessKeyEncrypted,
		&target.CreatedAtMillis, &target.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupTarget{}, ErrBackupTargetNotFound
	}
	return target, err
}

func (store *Store) SetBackupTarget(ctx context.Context, input SetBackupTarget) (BackupTarget, error) {
	target := input.Target
	if target.Endpoint == "" || target.Region == "" || target.Bucket == "" || target.AccessKeyID == "" ||
		len(target.SecretAccessKeyEncrypted) == 0 || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return BackupTarget{}, errors.New("set backup target input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return BackupTarget{}, err
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var installationID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		var createdAt int64
		err := transaction.QueryRowContext(ctx, "SELECT created_at FROM backup_target WHERE singleton = 1").Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return err
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
  updated_at = excluded.updated_at`,
			target.Endpoint, target.Region, target.Bucket, target.Prefix,
			target.AccessKeyID, target.SecretAccessKeyEncrypted, createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("set backup target: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "endpoint": target.Endpoint,
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
			input.AuditEventID, input.ActorKind, input.ActorID, installationID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	if err != nil {
		return BackupTarget{}, err
	}
	return store.BackupTarget(ctx)
}

type DeleteBackupTarget struct {
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) DeleteBackupTarget(ctx context.Context, input DeleteBackupTarget) error {
	if input.AuditEventID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete backup target input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var installationID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM backup_target WHERE singleton = 1")
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
			input.AuditEventID, input.ActorKind, input.ActorID, installationID,
			nullableString(input.RequestCorrelationID), string(metadata), input.DeletedAtMillis)
		return err
	})
}

func (store *Store) EmbeddedObjectStoreHostnameExists(ctx context.Context, hostname string) (bool, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM object_stores WHERE public_hostname = ?)`, hostname).Scan(&exists)
	return exists == 1, err
}
