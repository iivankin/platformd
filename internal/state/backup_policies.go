package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/backupcron"
)

var (
	ErrBackupResourceNotFound = errors.New("backup resource not found")
	ErrInvalidBackupPolicy    = errors.New("invalid backup policy")
)

const DefaultBackupRetentionCount = 7

type InitialBackupPolicy struct {
	TargetID       string
	Enabled        bool
	Cron           string
	RetentionCount int
}

type BackupPolicy struct {
	ResourceKind   string
	ResourceID     string
	TargetID       string
	Enabled        bool
	Cron           string
	RetentionCount int
}

type SetBackupPolicy struct {
	ResourceKind         string
	ResourceID           string
	TargetID             string
	Enabled              bool
	Cron                 string
	RetentionCount       int
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func normalizeInitialBackupPolicy(input InitialBackupPolicy) (InitialBackupPolicy, error) {
	if input.RetentionCount == 0 {
		input.RetentionCount = DefaultBackupRetentionCount
	}
	if input.RetentionCount < 1 || input.RetentionCount > 100 {
		return InitialBackupPolicy{}, fmt.Errorf("%w: retention count must be between 1 and 100", ErrInvalidBackupPolicy)
	}
	var err error
	if input.Cron != "" {
		input.Cron, err = backupcron.Canonical(input.Cron)
		if err != nil {
			return InitialBackupPolicy{}, fmt.Errorf("%w: %v", ErrInvalidBackupPolicy, err)
		}
	}
	if input.Enabled && (input.TargetID == "" || input.Cron == "") {
		return InitialBackupPolicy{}, fmt.Errorf("%w: enabled policy requires a target and cron", ErrInvalidBackupPolicy)
	}
	return input, nil
}

func validateInitialBackupTarget(ctx context.Context, transaction *sql.Tx, targetID string) error {
	if targetID == "" {
		return nil
	}
	var exists int
	if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM backup_targets WHERE id = ?)`, targetID).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrBackupTargetNotFound
	}
	return nil
}

func (store *Store) BackupPolicies(ctx context.Context) ([]BackupPolicy, error) {
	const query = `
SELECT 'registry', id, backup_target_id, backup_enabled, backup_cron, backup_retention_count FROM registry_repositories
UNION ALL
SELECT 'object_store', id, backup_target_id, backup_enabled, backup_cron, backup_retention_count FROM object_stores
UNION ALL
SELECT 'postgres', id, backup_target_id, backup_enabled, backup_cron, backup_retention_count FROM managed_postgres
UNION ALL
SELECT 'redis', id, backup_target_id, backup_enabled, backup_cron, backup_retention_count FROM managed_redis
UNION ALL
SELECT 'volume', id, backup_target_id, backup_enabled, backup_cron, backup_retention_count FROM volumes
ORDER BY 1, 2`
	rows, err := store.database.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BackupPolicy, 0)
	for rows.Next() {
		policy, err := scanBackupPolicy(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

func (store *Store) BackupPolicy(ctx context.Context, resourceKind, resourceID string) (BackupPolicy, error) {
	table, err := backupResourceTable(resourceKind)
	if err != nil || resourceID == "" {
		return BackupPolicy{}, errors.New("backup policy identity is invalid")
	}
	row := store.database.QueryRowContext(ctx, `
SELECT ?, id, backup_target_id, backup_enabled, backup_cron, backup_retention_count
FROM `+table+` WHERE id = ?`, resourceKind, resourceID)
	policy, err := scanBackupPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupPolicy{}, ErrBackupResourceNotFound
	}
	return policy, err
}

func (store *Store) SetBackupPolicy(ctx context.Context, input SetBackupPolicy) (BackupPolicy, error) {
	table, err := backupResourceTable(input.ResourceKind)
	if err != nil || input.ResourceID == "" || input.RetentionCount < 1 || input.RetentionCount > 100 ||
		input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return BackupPolicy{}, errors.New("set backup policy input is invalid")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return BackupPolicy{}, err
	}
	cron := ""
	if input.Cron != "" {
		cron, err = backupcron.Canonical(input.Cron)
		if err != nil {
			return BackupPolicy{}, err
		}
	}
	if input.Enabled && (cron == "" || input.TargetID == "") {
		return BackupPolicy{}, errors.New("enabled backup policy requires a target and cron")
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": input.ActorEmail, "enabled": input.Enabled,
		"cron": cron, "retentionCount": input.RetentionCount, "targetId": input.TargetID,
	})
	if err != nil {
		return BackupPolicy{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
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
		enabled := 0
		if input.Enabled {
			enabled = 1
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE `+table+`
SET backup_target_id = ?, backup_enabled = ?, backup_cron = ?, backup_retention_count = ?, updated_at = ?
WHERE id = ?`, nullableString(input.TargetID), enabled, nullableString(cron), input.RetentionCount,
			input.UpdatedAtMillis, input.ResourceID)
		if err != nil {
			return fmt.Errorf("update backup policy: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrBackupResourceNotFound
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'backup.policy.set', ?, ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ResourceKind, input.ResourceID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	if err != nil {
		return BackupPolicy{}, err
	}
	return store.BackupPolicy(ctx, input.ResourceKind, input.ResourceID)
}

type backupPolicyScanner interface {
	Scan(...any) error
}

func scanBackupPolicy(scanner backupPolicyScanner) (BackupPolicy, error) {
	var result BackupPolicy
	var enabled int
	var targetID, cron sql.NullString
	err := scanner.Scan(&result.ResourceKind, &result.ResourceID, &targetID, &enabled, &cron, &result.RetentionCount)
	if err != nil {
		return BackupPolicy{}, err
	}
	result.Enabled = enabled == 1
	result.TargetID = targetID.String
	result.Cron = cron.String
	return result, nil
}

func backupResourceTable(kind string) (string, error) {
	switch kind {
	case "registry":
		return "registry_repositories", nil
	case "object_store":
		return "object_stores", nil
	case "postgres":
		return "managed_postgres", nil
	case "redis":
		return "managed_redis", nil
	case "volume":
		return "volumes", nil
	default:
		return "", errors.New("backup resource kind is invalid")
	}
}
