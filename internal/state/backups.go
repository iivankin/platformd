package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrBackupNotRunning = errors.New("backup is not running")

type BackupRecord struct {
	ID                        string
	TargetID                  string
	ResourceKind              string
	ResourceID                string
	ScheduledOccurrenceMillis *int64
	GenerationID              string
	Status                    string
	SizeBytes                 *int64
	ErrorCode                 string
	ErrorMessage              string
	StartedAtMillis           int64
	FinishedAtMillis          *int64
}

type BeginBackup struct {
	ID                        string
	TargetID                  string
	ResourceKind              string
	ResourceID                string
	ScheduledOccurrenceMillis *int64
	GenerationID              string
	StartedAtMillis           int64
}

type FinishBackup struct {
	ID               string
	Status           string
	SizeBytes        int64
	ErrorCode        string
	ErrorMessage     string
	FinishedAtMillis int64
}

type BackupHistoryQuery struct {
	TargetID     string
	ResourceKind string
	ResourceID   string
	BeforeMillis int64
	Limit        int
}

func (store *Store) BeginBackup(ctx context.Context, input BeginBackup) error {
	if input.ID == "" || input.TargetID == "" || !validBackupResourceKind(input.ResourceKind) || input.ResourceID == "" ||
		input.GenerationID == "" || input.StartedAtMillis <= 0 ||
		(input.ScheduledOccurrenceMillis != nil && *input.ScheduledOccurrenceMillis <= 0) {
		return errors.New("begin backup input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO backups(
  id, target_id, resource_kind, resource_id, scheduled_occurrence, generation_id, status, started_at
) VALUES (?, ?, ?, ?, ?, ?, 'running', ?)`,
			input.ID, input.TargetID, input.ResourceKind, input.ResourceID, input.ScheduledOccurrenceMillis,
			input.GenerationID, input.StartedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("begin backup: %w", err)
		}
		return nil
	})
}

func (store *Store) FinishBackup(ctx context.Context, input FinishBackup) error {
	if input.ID == "" || input.FinishedAtMillis <= 0 || input.SizeBytes < 0 ||
		(input.Status != "succeeded" && input.Status != "failed") || len(input.ErrorCode) > 128 || len(input.ErrorMessage) > 4096 ||
		(input.Status == "succeeded" && (input.ErrorCode != "" || input.ErrorMessage != "")) ||
		(input.Status == "failed" && input.ErrorCode == "") {
		return errors.New("finish backup input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE backups
SET status = ?, size_bytes = ?, error_code = ?, error_message = ?, finished_at = ?
WHERE id = ? AND status = 'running'`,
			input.Status, input.SizeBytes, nullableString(input.ErrorCode), nullableString(input.ErrorMessage),
			input.FinishedAtMillis, input.ID,
		)
		if err != nil {
			return fmt.Errorf("finish backup: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrBackupNotRunning
		}
		return nil
	})
}

func (store *Store) Backup(ctx context.Context, id string) (BackupRecord, error) {
	var record BackupRecord
	var scheduled, size, finished sql.NullInt64
	var generation, errorCode, errorMessage sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT id, target_id, resource_kind, resource_id, scheduled_occurrence, generation_id,
       status, size_bytes, error_code, error_message, started_at, finished_at
FROM backups WHERE id = ?`, id).Scan(
		&record.ID, &record.TargetID, &record.ResourceKind, &record.ResourceID, &scheduled, &generation,
		&record.Status, &size, &errorCode, &errorMessage, &record.StartedAtMillis, &finished,
	)
	if err != nil {
		return BackupRecord{}, err
	}
	if scheduled.Valid {
		record.ScheduledOccurrenceMillis = &scheduled.Int64
	}
	if generation.Valid {
		record.GenerationID = generation.String
	}
	if size.Valid {
		record.SizeBytes = &size.Int64
	}
	if errorCode.Valid {
		record.ErrorCode = errorCode.String
	}
	if errorMessage.Valid {
		record.ErrorMessage = errorMessage.String
	}
	if finished.Valid {
		record.FinishedAtMillis = &finished.Int64
	}
	return record, nil
}

func (store *Store) BackupHistory(ctx context.Context, query BackupHistoryQuery) ([]BackupRecord, error) {
	if !validBackupResourceKind(query.ResourceKind) || query.ResourceID == "" || query.Limit < 1 || query.Limit > 100 {
		return nil, errors.New("backup history query is invalid")
	}
	before := query.BeforeMillis
	if before <= 0 {
		before = int64(^uint64(0) >> 1)
	}
	queryText := `
SELECT id FROM backups
WHERE resource_kind = ? AND resource_id = ? AND started_at < ?
ORDER BY started_at DESC, id DESC LIMIT ?`
	arguments := []any{query.ResourceKind, query.ResourceID, before, query.Limit}
	if query.TargetID != "" {
		queryText = `
SELECT id FROM backups
WHERE target_id = ? AND resource_kind = ? AND resource_id = ? AND started_at < ?
ORDER BY started_at DESC, id DESC LIMIT ?`
		arguments = []any{query.TargetID, query.ResourceKind, query.ResourceID, before, query.Limit}
	}
	rows, err := store.database.QueryContext(ctx, queryText, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, query.Limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]BackupRecord, 0, len(ids))
	for _, id := range ids {
		record, err := store.Backup(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (store *Store) ScheduledBackupExists(ctx context.Context, resourceKind, resourceID string, occurrenceMillis int64) (bool, error) {
	if !validBackupResourceKind(resourceKind) || resourceKind == "control" || resourceID == "" || occurrenceMillis <= 0 {
		return false, errors.New("scheduled backup identity is invalid")
	}
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM backups
  WHERE resource_kind = ? AND resource_id = ? AND scheduled_occurrence = ?
)`, resourceKind, resourceID, occurrenceMillis).Scan(&exists)
	return exists == 1, err
}

func validBackupResourceKind(value string) bool {
	switch value {
	case "control", "registry", "object_store", "postgres", "redis", "volume":
		return true
	default:
		return false
	}
}
