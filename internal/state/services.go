package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

var (
	ErrResourceNameConflict        = errors.New("resource name already exists in project")
	ErrImageCredentialNotFound     = errors.New("image registry credential not found in project")
	ErrImageCredentialHostMismatch = errors.New("image registry credential host does not match image")
)

type ServiceDesired struct {
	ID                 string
	ProjectID          string
	ProjectName        string
	Name               string
	Enabled            bool
	ActiveDeploymentID string
	ActiveImageDigest  string
	ActiveConfigHash   string
	CreatedAtMillis    int64
	UpdatedAtMillis    int64
	Snapshot           serviceconfig.Snapshot
}

type CreateService struct {
	ID                   string
	ProjectID            string
	Name                 string
	Enabled              bool
	Snapshot             serviceconfig.Snapshot
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) CreateService(ctx context.Context, input CreateService) (ServiceDesired, error) {
	if input.ID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.CreatedAtMillis <= 0 {
		return ServiceDesired{}, errors.New("create service input is incomplete")
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ServiceDesired{}, err
	}
	snapshot, _, _, err := serviceconfig.Canonical(input.Snapshot)
	if err != nil {
		return ServiceDesired{}, err
	}
	commandJSON, err := optionalStringSliceJSON(snapshot.Command)
	if err != nil {
		return ServiceDesired{}, err
	}
	argsJSON, err := optionalStringSliceJSON(snapshot.Args)
	if err != nil {
		return ServiceDesired{}, err
	}
	environmentJSON, err := json.Marshal(snapshot.Environment)
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("encode service environment: %w", err)
	}
	metadataJSON, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
	if err != nil {
		return ServiceDesired{}, err
	}

	err = store.Write(ctx, func(transaction *sql.Tx) error {
		var projectName string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM projects WHERE id = ?", input.ProjectID).Scan(&projectName); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return fmt.Errorf("load service project: %w", err)
		}
		if exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, input.Name); err != nil {
			return err
		} else if exists {
			return ErrResourceNameConflict
		}
		if snapshot.ImageCredentialID != "" {
			var credentialID string
			var credentialProjectID string
			var credentialHost string
			if err := transaction.QueryRowContext(ctx, "SELECT id, project_id, registry_host FROM image_registry_credentials WHERE id = ?", snapshot.ImageCredentialID).Scan(&credentialID, &credentialProjectID, &credentialHost); errors.Is(err, sql.ErrNoRows) {
				return ErrImageCredentialNotFound
			} else if err != nil {
				return fmt.Errorf("load image registry credential: %w", err)
			} else if credentialProjectID != input.ProjectID {
				return ErrImageCredentialNotFound
			}
			imageHost, err := imagecredential.HostForReference(snapshot.ImageReference)
			if err != nil {
				return err
			}
			if credentialHost != imageHost {
				return fmt.Errorf("%w: credential is for %s, image uses %s", ErrImageCredentialHostMismatch, credentialHost, imageHost)
			}
		}
		if len(snapshot.VolumeMounts) != 0 {
			return errors.New("volumes must be created after their service")
		}
		for _, reference := range snapshot.SecretReferences {
			var projectID string
			if err := transaction.QueryRowContext(ctx, "SELECT project_id FROM secrets WHERE id = ?", reference.SecretID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("secret %s does not exist", reference.SecretID)
			} else if err != nil {
				return fmt.Errorf("load secret %s: %w", reference.SecretID, err)
			} else if projectID != input.ProjectID {
				return fmt.Errorf("secret %s belongs to another project", reference.SecretID)
			}
		}

		var targetPort any
		if snapshot.TargetPort != nil {
			targetPort = *snapshot.TargetPort
		}
		var imageCredentialID any
		if snapshot.ImageCredentialID != "" {
			imageCredentialID = snapshot.ImageCredentialID
		}
		var healthPath any
		if snapshot.HealthPath != "" {
			healthPath = snapshot.HealthPath
		}
		var cpuMillis any
		if snapshot.CPUMillicores > 0 {
			cpuMillis = snapshot.CPUMillicores
		}
		var memoryBytes any
		if snapshot.MemoryMaxBytes > 0 {
			memoryBytes = snapshot.MemoryMaxBytes
		}
		enabled := 0
		if input.Enabled {
			enabled = 1
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO services(
  id, project_id, name, image_reference, image_credential_id,
  command_json, args_json, environment_json, target_port, health_path,
  startup_timeout_seconds, cpu_millis, memory_bytes, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ProjectID, input.Name, snapshot.ImageReference, imageCredentialID,
			commandJSON, argsJSON, string(environmentJSON), targetPort, healthPath,
			snapshot.StartupTimeoutSeconds, cpuMillis, memoryBytes, enabled,
			input.CreatedAtMillis, input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create service: %w", err)
		}
		for _, reference := range snapshot.SecretReferences {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_secret_refs(service_id, environment_name, secret_id)
VALUES (?, ?, ?)`, input.ID, reference.EnvironmentName, reference.SecretID); err != nil {
				return fmt.Errorf("create service secret reference: %w", err)
			}
		}
		var correlationID any
		if input.RequestCorrelationID != "" {
			correlationID = input.RequestCorrelationID
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'service.create', 'service', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, input.ID, correlationID, string(metadataJSON), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit service creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.DesiredService(ctx, input.ID)
}

func (store *Store) DesiredService(ctx context.Context, serviceID string) (ServiceDesired, error) {
	var service ServiceDesired
	var enabled int
	var activeDeploymentID sql.NullString
	var activeImageDigest sql.NullString
	var activeConfigHash sql.NullString
	var imageCredentialID sql.NullString
	var commandJSON sql.NullString
	var argsJSON sql.NullString
	var environmentJSON string
	var targetPort sql.NullInt64
	var healthPath sql.NullString
	var cpuMillis sql.NullInt64
	var memoryBytes sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT s.id, s.project_id, p.name, s.name, s.enabled, s.active_deployment_id,
       d.image_digest, d.service_config_hash,
       s.image_reference, s.image_credential_id, s.command_json, s.args_json,
       s.environment_json, s.target_port, s.health_path, s.startup_timeout_seconds,
       s.cpu_millis, s.memory_bytes, s.created_at, s.updated_at
FROM services s
JOIN projects p ON p.id = s.project_id
LEFT JOIN deployments d ON d.id = s.active_deployment_id
WHERE s.id = ?`, serviceID).Scan(
		&service.ID, &service.ProjectID, &service.ProjectName, &service.Name, &enabled,
		&activeDeploymentID, &activeImageDigest, &activeConfigHash,
		&service.Snapshot.ImageReference, &imageCredentialID, &commandJSON, &argsJSON,
		&environmentJSON, &targetPort, &healthPath, &service.Snapshot.StartupTimeoutSeconds,
		&cpuMillis, &memoryBytes, &service.CreatedAtMillis, &service.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDesired{}, sql.ErrNoRows
	}
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("load desired service: %w", err)
	}
	service.Enabled = enabled == 1
	service.ActiveDeploymentID = activeDeploymentID.String
	service.ActiveImageDigest = activeImageDigest.String
	service.ActiveConfigHash = activeConfigHash.String
	service.Snapshot.ImageCredentialID = imageCredentialID.String
	service.Snapshot.HealthPath = healthPath.String
	service.Snapshot.CPUMillicores = cpuMillis.Int64
	service.Snapshot.MemoryMaxBytes = memoryBytes.Int64
	if targetPort.Valid {
		port := int(targetPort.Int64)
		service.Snapshot.TargetPort = &port
	}
	if commandJSON.Valid {
		if err := json.Unmarshal([]byte(commandJSON.String), &service.Snapshot.Command); err != nil {
			return ServiceDesired{}, fmt.Errorf("decode service command: %w", err)
		}
	}
	if argsJSON.Valid {
		if err := json.Unmarshal([]byte(argsJSON.String), &service.Snapshot.Args); err != nil {
			return ServiceDesired{}, fmt.Errorf("decode service args: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(environmentJSON), &service.Snapshot.Environment); err != nil {
		return ServiceDesired{}, fmt.Errorf("decode service environment: %w", err)
	}
	secretRows, err := store.database.QueryContext(ctx, `
SELECT environment_name, secret_id FROM service_secret_refs
WHERE service_id = ? ORDER BY environment_name, secret_id`, serviceID)
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("list service secret references: %w", err)
	}
	for secretRows.Next() {
		var reference serviceconfig.SecretReference
		if err := secretRows.Scan(&reference.EnvironmentName, &reference.SecretID); err != nil {
			secretRows.Close()
			return ServiceDesired{}, fmt.Errorf("scan service secret reference: %w", err)
		}
		service.Snapshot.SecretReferences = append(service.Snapshot.SecretReferences, reference)
	}
	if err := secretRows.Err(); err != nil {
		secretRows.Close()
		return ServiceDesired{}, fmt.Errorf("iterate service secret references: %w", err)
	}
	if err := secretRows.Close(); err != nil {
		return ServiceDesired{}, fmt.Errorf("close service secret references: %w", err)
	}
	volumeRows, err := store.database.QueryContext(ctx, `
SELECT m.volume_id, m.container_path FROM service_volume_mounts m
WHERE m.service_id = ? ORDER BY m.container_path, m.volume_id`, serviceID)
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("list service volume mounts: %w", err)
	}
	for volumeRows.Next() {
		var mount serviceconfig.VolumeMount
		if err := volumeRows.Scan(&mount.VolumeID, &mount.ContainerPath); err != nil {
			volumeRows.Close()
			return ServiceDesired{}, fmt.Errorf("scan service volume mount: %w", err)
		}
		service.Snapshot.VolumeMounts = append(service.Snapshot.VolumeMounts, mount)
	}
	if err := volumeRows.Err(); err != nil {
		volumeRows.Close()
		return ServiceDesired{}, fmt.Errorf("iterate service volume mounts: %w", err)
	}
	if err := volumeRows.Close(); err != nil {
		return ServiceDesired{}, fmt.Errorf("close service volume mounts: %w", err)
	}
	normalized, err := serviceconfig.Normalize(service.Snapshot)
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("validate stored service snapshot: %w", err)
	}
	service.Snapshot = normalized
	return service, nil
}

func (store *Store) EnabledServiceIDs(ctx context.Context) ([]string, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM services WHERE enabled = 1 ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list enabled services: %w", err)
	}
	defer rows.Close()
	serviceIDs := make([]string, 0)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, fmt.Errorf("scan enabled service: %w", err)
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled services: %w", err)
	}
	return serviceIDs, nil
}

func projectResourceNameExists(ctx context.Context, transaction *sql.Tx, projectID, name string) (bool, error) {
	var exists int
	err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM services WHERE project_id = ? AND name = ?
  UNION ALL SELECT 1 FROM managed_postgres WHERE project_id = ? AND name = ?
  UNION ALL SELECT 1 FROM managed_redis WHERE project_id = ? AND name = ?
  UNION ALL SELECT 1 FROM object_stores WHERE project_id = ? AND name = ?
)`, projectID, name, projectID, name, projectID, name, projectID, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check project resource name: %w", err)
	}
	return exists == 1, nil
}

func optionalStringSliceJSON(values []string) (any, error) {
	if values == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode string list: %w", err)
	}
	return string(encoded), nil
}
