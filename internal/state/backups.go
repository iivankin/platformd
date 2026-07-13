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

func (store *Store) BeginBackup(ctx context.Context, input BeginBackup) error {
	if input.ID == "" || !validBackupResourceKind(input.ResourceKind) || input.ResourceID == "" ||
		input.GenerationID == "" || input.StartedAtMillis <= 0 ||
		(input.ScheduledOccurrenceMillis != nil && *input.ScheduledOccurrenceMillis <= 0) {
		return errors.New("begin backup input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO backups(
  id, resource_kind, resource_id, scheduled_occurrence, generation_id, status, started_at
) VALUES (?, ?, ?, ?, ?, 'running', ?)`,
			input.ID, input.ResourceKind, input.ResourceID, input.ScheduledOccurrenceMillis,
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
SELECT id, resource_kind, resource_id, scheduled_occurrence, generation_id,
       status, size_bytes, error_code, error_message, started_at, finished_at
FROM backups WHERE id = ?`, id).Scan(
		&record.ID, &record.ResourceKind, &record.ResourceID, &scheduled, &generation,
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

func validBackupResourceKind(value string) bool {
	switch value {
	case "control", "registry", "object_store", "postgres", "redis":
		return true
	default:
		return false
	}
}
