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

var ErrManagedRedisNotFound = errors.New("managed Redis resource not found")

type ManagedRedis struct {
	ID                   string
	ProjectID            string
	ProjectName          string
	Name                 string
	ImageTag             string
	ImageDigest          string
	VolumeID             string
	PasswordEncrypted    []byte
	CPUMillicores        int64
	MemoryMaxBytes       int64
	BackupEnabled        bool
	BackupCron           string
	BackupRetentionCount int
	CreatedAtMillis      int64
	UpdatedAtMillis      int64
}

type CreateManagedRedis struct {
	ID                   string
	ProjectID            string
	Name                 string
	ImageTag             string
	ImageDigest          string
	VolumeID             string
	PasswordEncrypted    []byte
	CPUMillicores        int64
	MemoryMaxBytes       int64
	BackupPolicy         InitialBackupPolicy
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) CreateManagedRedis(ctx context.Context, input CreateManagedRedis) (ManagedRedis, error) {
	input.ImageTag = strings.TrimSpace(input.ImageTag)
	if input.ID == "" || input.ProjectID == "" || input.VolumeID == "" || len(input.PasswordEncrypted) == 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return ManagedRedis{}, errors.New("create managed Redis input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return ManagedRedis{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ManagedRedis{}, err
	}
	if _, err := managedimages.Reference(managedimages.Redis, input.ImageTag); err != nil {
		return ManagedRedis{}, err
	}
	parsedDigest, err := digest.Parse(input.ImageDigest)
	if err != nil || parsedDigest.Validate() != nil {
		return ManagedRedis{}, errors.New("managed Redis image digest is invalid")
	}
	if input.CPUMillicores < 0 || input.MemoryMaxBytes < 0 {
		return ManagedRedis{}, errors.New("managed Redis resource limits cannot be negative")
	}
	backupPolicy, err := normalizeInitialBackupPolicy(input.BackupPolicy)
	if err != nil {
		return ManagedRedis{}, err
	}
	metadata := make(map[string]string)
	if input.ActorEmail != "" {
		metadata["actorEmail"] = input.ActorEmail
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return ManagedRedis{}, err
	}

	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var projectID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE id = ?", input.ProjectID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return fmt.Errorf("load managed Redis project: %w", err)
		}
		exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, input.Name)
		if err != nil {
			return err
		}
		if exists {
			return ErrResourceNameConflict
		}
		if err := validateInitialBackupTarget(ctx, transaction, backupPolicy.TargetID); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO managed_redis(
  id, project_id, name, image_tag, image_digest, volume_id, password_encrypted,
  cpu_millis, memory_bytes, backup_target_id, backup_enabled, backup_cron,
  backup_retention_count, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ProjectID, input.Name, input.ImageTag, input.ImageDigest,
			input.VolumeID, input.PasswordEncrypted, nullablePositive(input.CPUMillicores),
			nullablePositive(input.MemoryMaxBytes), nullableString(backupPolicy.TargetID),
			boolInteger(backupPolicy.Enabled), nullableString(backupPolicy.Cron),
			backupPolicy.RetentionCount, input.CreatedAtMillis, input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create managed Redis resource: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'redis.create', 'redis', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadataJSON), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit managed Redis creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ManagedRedis{}, err
	}
	return store.ManagedRedis(ctx, input.ID)
}

func (store *Store) ManagedRedis(ctx context.Context, resourceID string) (ManagedRedis, error) {
	return store.managedRedis(ctx, resourceID, "")
}

func (store *Store) ManagedRedisInProject(ctx context.Context, projectID, resourceID string) (ManagedRedis, error) {
	if projectID == "" {
		return ManagedRedis{}, ErrManagedRedisNotFound
	}
	return store.managedRedis(ctx, resourceID, projectID)
}

func (store *Store) managedRedis(ctx context.Context, resourceID, projectID string) (ManagedRedis, error) {
	var resource ManagedRedis
	var cpuMillis sql.NullInt64
	var memoryBytes sql.NullInt64
	var backupEnabled int
	var backupCron sql.NullString
	query := `
SELECT r.id, r.project_id, p.name, r.name, r.image_tag, r.image_digest,
       r.volume_id, r.password_encrypted, r.cpu_millis, r.memory_bytes,
       r.backup_enabled, r.backup_cron, r.backup_retention_count,
       r.created_at, r.updated_at
FROM managed_redis r
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
		&resource.PasswordEncrypted, &cpuMillis, &memoryBytes, &backupEnabled,
		&backupCron, &resource.BackupRetentionCount, &resource.CreatedAtMillis,
		&resource.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedRedis{}, ErrManagedRedisNotFound
	}
	if err != nil {
		return ManagedRedis{}, fmt.Errorf("load managed Redis resource: %w", err)
	}
	resource.CPUMillicores = cpuMillis.Int64
	resource.MemoryMaxBytes = memoryBytes.Int64
	resource.BackupEnabled = backupEnabled == 1
	resource.BackupCron = backupCron.String
	return resource, nil
}

func (store *Store) ManagedRedisByProject(ctx context.Context, projectID string) ([]ManagedRedis, error) {
	var exists int
	if err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", projectID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check managed Redis project: %w", err)
	}
	if exists == 0 {
		return nil, ErrProjectNotFound
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT r.id FROM managed_redis r WHERE r.project_id = ? ORDER BY r.name, r.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list managed Redis resources: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan managed Redis resource: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed Redis resources: %w", err)
	}
	resources := make([]ManagedRedis, 0, len(ids))
	for _, id := range ids {
		resource, err := store.ManagedRedis(ctx, id)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (store *Store) ManagedRedisResources(ctx context.Context) ([]ManagedRedis, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT id FROM managed_redis ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list all managed Redis resources: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan managed Redis resource ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed Redis resource IDs: %w", err)
	}
	resources := make([]ManagedRedis, 0, len(ids))
	for _, id := range ids {
		resource, err := store.ManagedRedis(ctx, id)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
