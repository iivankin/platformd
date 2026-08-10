package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/resourcename"
)

var (
	ErrErrorTrackerNotFound = errors.New("error tracker not found")
	ErrErrorTrackerChanged  = errors.New("error tracker changed")
)

type ErrorTracker struct {
	ID                   string
	ProjectID            string
	ProjectName          string
	Name                 string
	VolumeID             string
	PublicHostname       string
	BackupEnabled        bool
	BackupCron           string
	BackupRetentionCount int
	CreatedAtMillis      int64
	UpdatedAtMillis      int64
}

type CreateErrorTracker struct {
	ID                   string
	ProjectID            string
	Name                 string
	VolumeID             string
	PublicHostname       string
	BackupPolicy         InitialBackupPolicy
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) CreateErrorTracker(ctx context.Context, input CreateErrorTracker) (ErrorTracker, error) {
	if input.ID == "" || input.ProjectID == "" || input.VolumeID == "" || input.AuditEventID == "" ||
		input.CreatedAtMillis <= 0 {
		return ErrorTracker{}, errors.New("create error tracker input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return ErrorTracker{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ErrorTracker{}, err
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ErrorTracker{}, err
		}
		input.PublicHostname = hostname
	}
	policy, err := normalizeInitialBackupPolicy(input.BackupPolicy)
	if err != nil {
		return ErrorTracker{}, err
	}
	metadata, err := json.Marshal(map[string]string{
		"actorEmail": input.ActorEmail,
		"name":       input.Name,
	})
	if err != nil {
		return ErrorTracker{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var projectID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE id = ?", input.ProjectID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return fmt.Errorf("load error tracker project: %w", err)
		}
		if exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, input.Name); err != nil {
			return err
		} else if exists {
			return ErrResourceNameConflict
		}
		if err := validateInitialBackupTarget(ctx, transaction, policy.TargetID); err != nil {
			return err
		}
		if input.PublicHostname != "" {
			inUse, err := publicHostnameRoleExists(ctx, transaction, input.PublicHostname)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO error_trackers(
  id, project_id, name, volume_id, public_hostname,
  backup_target_id, backup_enabled, backup_cron, backup_retention_count,
  created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ProjectID, input.Name, input.VolumeID, nullableString(input.PublicHostname),
			nullableString(policy.TargetID), boolInteger(policy.Enabled), nullableString(policy.Cron),
			policy.RetentionCount, input.CreatedAtMillis, input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create error tracker: %w", err)
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'error_tracker.create', 'error_tracker', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ProjectID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis,
		)
		return err
	})
	if err != nil {
		return ErrorTracker{}, err
	}
	return store.ErrorTrackerInProject(ctx, input.ProjectID, input.ID)
}

const errorTrackerSelect = `
SELECT t.id, t.project_id, p.name, t.name, t.volume_id, t.public_hostname,
       t.backup_enabled, t.backup_cron, t.backup_retention_count,
       t.created_at, t.updated_at
FROM error_trackers t JOIN projects p ON p.id = t.project_id`

func (store *Store) ErrorTracker(ctx context.Context, trackerID string) (ErrorTracker, error) {
	return store.errorTracker(ctx, "", trackerID)
}

func (store *Store) ErrorTrackerInProject(ctx context.Context, projectID, trackerID string) (ErrorTracker, error) {
	if projectID == "" {
		return ErrorTracker{}, ErrErrorTrackerNotFound
	}
	return store.errorTracker(ctx, projectID, trackerID)
}

func (store *Store) errorTracker(ctx context.Context, projectID, trackerID string) (ErrorTracker, error) {
	query := errorTrackerSelect + " WHERE t.id = ?"
	arguments := []any{trackerID}
	if projectID != "" {
		query += " AND t.project_id = ?"
		arguments = append(arguments, projectID)
	}
	tracker, err := scanErrorTracker(store.database.QueryRowContext(ctx, query, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrorTracker{}, ErrErrorTrackerNotFound
	}
	if err != nil {
		return ErrorTracker{}, fmt.Errorf("load error tracker: %w", err)
	}
	return tracker, nil
}

func (store *Store) ErrorTrackerByHostname(ctx context.Context, hostname string) (ErrorTracker, error) {
	tracker, err := scanErrorTracker(store.database.QueryRowContext(
		ctx, errorTrackerSelect+" WHERE t.public_hostname = ?", hostname,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrorTracker{}, ErrErrorTrackerNotFound
	}
	return tracker, err
}

func (store *Store) ErrorTrackers(ctx context.Context) ([]ErrorTracker, error) {
	return store.errorTrackers(ctx, "")
}

func (store *Store) ErrorTrackersByProject(ctx context.Context, projectID string) ([]ErrorTracker, error) {
	return store.errorTrackers(ctx, projectID)
}

func (store *Store) errorTrackers(ctx context.Context, projectID string) ([]ErrorTracker, error) {
	query := errorTrackerSelect
	arguments := []any{}
	if projectID != "" {
		var exists int
		if err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", projectID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, ErrProjectNotFound
		}
		query += " WHERE t.project_id = ?"
		arguments = append(arguments, projectID)
	}
	query += " ORDER BY p.name, t.name, t.id"
	rows, err := store.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list error trackers: %w", err)
	}
	defer rows.Close()
	result := make([]ErrorTracker, 0)
	for rows.Next() {
		tracker, err := scanErrorTracker(rows)
		if err != nil {
			return nil, fmt.Errorf("scan error tracker: %w", err)
		}
		result = append(result, tracker)
	}
	return result, rows.Err()
}

type errorTrackerScanner interface{ Scan(...any) error }

func scanErrorTracker(scanner errorTrackerScanner) (ErrorTracker, error) {
	var tracker ErrorTracker
	var publicHostname, backupCron sql.NullString
	var backupEnabled int
	err := scanner.Scan(
		&tracker.ID, &tracker.ProjectID, &tracker.ProjectName, &tracker.Name, &tracker.VolumeID,
		&publicHostname, &backupEnabled, &backupCron, &tracker.BackupRetentionCount,
		&tracker.CreatedAtMillis, &tracker.UpdatedAtMillis,
	)
	tracker.PublicHostname = publicHostname.String
	tracker.BackupEnabled = backupEnabled == 1
	tracker.BackupCron = backupCron.String
	return tracker, err
}

type UpdateErrorTrackerPublicAccess struct {
	ID                    string
	ProjectID             string
	PublicHostname        string
	ExpectedUpdatedMillis int64
	UpdatedAtMillis       int64
}

func (store *Store) UpdateErrorTrackerPublicAccess(ctx context.Context, input UpdateErrorTrackerPublicAccess) (ErrorTracker, error) {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 || input.UpdatedAtMillis <= 0 {
		return ErrorTracker{}, errors.New("update error tracker public access input is incomplete")
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ErrorTracker{}, err
		}
		input.PublicHostname = hostname
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if input.PublicHostname != "" {
			inUse, err := publicHostnameRoleExistsExceptErrorTracker(ctx, transaction, input.PublicHostname, input.ID)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE error_trackers SET public_hostname = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			nullableString(input.PublicHostname),
			monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis),
			input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("update error tracker public access: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrErrorTrackerChanged
		}
		return nil
	})
	if err != nil {
		return ErrorTracker{}, err
	}
	return store.ErrorTrackerInProject(ctx, input.ProjectID, input.ID)
}
