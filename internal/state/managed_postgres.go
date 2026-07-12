package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/opencontainers/go-digest"
)

var ErrManagedPostgresNotFound = errors.New("managed PostgreSQL resource not found")

type ManagedPostgres struct {
	ID                         string
	ProjectID                  string
	ProjectName                string
	Name                       string
	ImageTag                   string
	ImageDigest                string
	VolumeID                   string
	DatabaseName               string
	OwnerUsername              string
	OwnerPasswordEncrypted     []byte
	BootstrapPasswordEncrypted []byte
	CPUMillicores              int64
	MemoryMaxBytes             int64
	BackupEnabled              bool
	BackupCron                 string
	BackupRetentionCount       int
	CreatedAtMillis            int64
	UpdatedAtMillis            int64
}

type CreateManagedPostgres struct {
	ID                         string
	ProjectID                  string
	Name                       string
	ImageTag                   string
	ImageDigest                string
	VolumeID                   string
	DatabaseName               string
	OwnerUsername              string
	OwnerPasswordEncrypted     []byte
	BootstrapPasswordEncrypted []byte
	CPUMillicores              int64
	MemoryMaxBytes             int64
	AuditEventID               string
	ActorKind                  string
	ActorID                    string
	ActorEmail                 string
	RequestCorrelationID       string
	CreatedAtMillis            int64
}

func (store *Store) CreateManagedPostgres(ctx context.Context, input CreateManagedPostgres) (ManagedPostgres, error) {
	input.ImageTag = strings.TrimSpace(input.ImageTag)
	if input.ID == "" || input.ProjectID == "" || input.VolumeID == "" || input.DatabaseName == "" || input.OwnerUsername == "" || len(input.OwnerPasswordEncrypted) == 0 || len(input.BootstrapPasswordEncrypted) == 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return ManagedPostgres{}, errors.New("create managed PostgreSQL input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return ManagedPostgres{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ManagedPostgres{}, err
	}
	if _, err := managedimages.Reference(managedimages.PostgreSQL, input.ImageTag); err != nil {
		return ManagedPostgres{}, err
	}
	parsedDigest, err := digest.Parse(input.ImageDigest)
	if err != nil || parsedDigest.Validate() != nil {
		return ManagedPostgres{}, errors.New("managed PostgreSQL image digest is invalid")
	}
	if input.CPUMillicores < 0 || input.MemoryMaxBytes < 0 {
		return ManagedPostgres{}, errors.New("managed PostgreSQL resource limits cannot be negative")
	}
	metadata := make(map[string]string)
	if input.ActorEmail != "" {
		metadata["actorEmail"] = input.ActorEmail
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return ManagedPostgres{}, err
	}
	err = store.Write(ctx, func(transaction *sql.Tx) error {
		var projectID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE id = ?", input.ProjectID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return fmt.Errorf("load managed PostgreSQL project: %w", err)
		}
		exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, input.Name)
		if err != nil {
			return err
		}
		if exists {
			return ErrResourceNameConflict
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO managed_postgres(
  id, project_id, name, image_tag, image_digest, volume_id, database_name,
  owner_username, owner_password_encrypted, bootstrap_password_encrypted,
  cpu_millis, memory_bytes, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ProjectID, input.Name, input.ImageTag, input.ImageDigest,
			input.VolumeID, input.DatabaseName, input.OwnerUsername,
			input.OwnerPasswordEncrypted, input.BootstrapPasswordEncrypted,
			nullablePositive(input.CPUMillicores), nullablePositive(input.MemoryMaxBytes),
			input.CreatedAtMillis, input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create managed PostgreSQL resource: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'postgres.create', 'postgres', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadataJSON), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit managed PostgreSQL creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ManagedPostgres{}, err
	}
	return store.ManagedPostgres(ctx, input.ID)
}

func (store *Store) ManagedPostgres(ctx context.Context, resourceID string) (ManagedPostgres, error) {
	return store.managedPostgres(ctx, resourceID, "")
}

func (store *Store) ManagedPostgresInProject(ctx context.Context, projectID, resourceID string) (ManagedPostgres, error) {
	if projectID == "" {
		return ManagedPostgres{}, ErrManagedPostgresNotFound
	}
	return store.managedPostgres(ctx, resourceID, projectID)
}

func (store *Store) managedPostgres(ctx context.Context, resourceID, projectID string) (ManagedPostgres, error) {
	var resource ManagedPostgres
	var cpuMillis, memoryBytes sql.NullInt64
	var backupEnabled int
	var backupCron sql.NullString
	query := `
SELECT r.id, r.project_id, p.name, r.name, r.image_tag, r.image_digest,
       r.volume_id, r.database_name, r.owner_username,
       r.owner_password_encrypted, r.bootstrap_password_encrypted,
       r.cpu_millis, r.memory_bytes, r.backup_enabled, r.backup_cron,
       r.backup_retention_count, r.created_at, r.updated_at
FROM managed_postgres r
JOIN projects p ON p.id = r.project_id
WHERE r.id = ?`
	arguments := []any{resourceID}
	if projectID != "" {
		query += " AND r.project_id = ?"
		arguments = append(arguments, projectID)
	}
	err := store.database.QueryRowContext(ctx, query, arguments...).Scan(
		&resource.ID, &resource.ProjectID, &resource.ProjectName, &resource.Name,
		&resource.ImageTag, &resource.ImageDigest, &resource.VolumeID,
		&resource.DatabaseName, &resource.OwnerUsername, &resource.OwnerPasswordEncrypted,
		&resource.BootstrapPasswordEncrypted, &cpuMillis, &memoryBytes, &backupEnabled,
		&backupCron, &resource.BackupRetentionCount, &resource.CreatedAtMillis, &resource.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedPostgres{}, ErrManagedPostgresNotFound
	}
	if err != nil {
		return ManagedPostgres{}, fmt.Errorf("load managed PostgreSQL resource: %w", err)
	}
	resource.CPUMillicores = cpuMillis.Int64
	resource.MemoryMaxBytes = memoryBytes.Int64
	resource.BackupEnabled = backupEnabled == 1
	resource.BackupCron = backupCron.String
	return resource, nil
}

func (store *Store) ManagedPostgresByProject(ctx context.Context, projectID string) ([]ManagedPostgres, error) {
	var exists int
	if err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", projectID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check managed PostgreSQL project: %w", err)
	}
	if exists == 0 {
		return nil, ErrProjectNotFound
	}
	return store.managedPostgresList(ctx, `SELECT id FROM managed_postgres WHERE project_id = ? ORDER BY name, id`, projectID)
}

func (store *Store) ManagedPostgresResources(ctx context.Context) ([]ManagedPostgres, error) {
	return store.managedPostgresList(ctx, `SELECT id FROM managed_postgres ORDER BY id`)
}

func (store *Store) managedPostgresList(ctx context.Context, query string, arguments ...any) ([]ManagedPostgres, error) {
	rows, err := store.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list managed PostgreSQL resources: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var resourceID string
		if err := rows.Scan(&resourceID); err != nil {
			return nil, fmt.Errorf("scan managed PostgreSQL resource ID: %w", err)
		}
		ids = append(ids, resourceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed PostgreSQL resource IDs: %w", err)
	}
	resources := make([]ManagedPostgres, 0, len(ids))
	for _, resourceID := range ids {
		resource, err := store.ManagedPostgres(ctx, resourceID)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
