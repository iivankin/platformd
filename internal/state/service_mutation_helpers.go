package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

func validateServiceMutationIdentity(serviceID, projectID string, expectedUpdated int64, auditID, actorID, actorEmail string, timestamp int64) error {
	if serviceID == "" || projectID == "" || expectedUpdated <= 0 || auditID == "" || actorID == "" || actorEmail == "" || timestamp <= 0 {
		return errors.New("service mutation input is incomplete")
	}
	return nil
}

func validateServiceVersion(ctx context.Context, transaction *sql.Tx, serviceID, projectID string, expectedUpdated int64) error {
	var updatedAt int64
	err := transaction.QueryRowContext(ctx, `
SELECT updated_at FROM services WHERE id = ? AND project_id = ?`, serviceID, projectID).Scan(&updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServiceNotFound
	}
	if err != nil {
		return fmt.Errorf("load service version: %w", err)
	}
	if updatedAt != expectedUpdated {
		return ErrServiceChanged
	}
	return nil
}

func validateServiceDependencies(ctx context.Context, transaction *sql.Tx, projectID, serviceID string, snapshot serviceconfig.Snapshot) error {
	if snapshot.ImageCredentialID != "" {
		var credentialProjectID string
		var credentialHost string
		err := transaction.QueryRowContext(ctx, `
SELECT project_id, registry_host FROM image_registry_credentials WHERE id = ?`, snapshot.ImageCredentialID).Scan(&credentialProjectID, &credentialHost)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: image credential %s", ErrDependencyMissing, snapshot.ImageCredentialID)
		}
		if err != nil {
			return fmt.Errorf("load image credential dependency: %w", err)
		}
		if credentialProjectID != projectID {
			return fmt.Errorf("%w: image credential %s", ErrDependencyMissing, snapshot.ImageCredentialID)
		}
		imageHost, err := imagecredential.HostForReference(snapshot.ImageReference)
		if err != nil {
			return err
		}
		if imageHost != credentialHost {
			return fmt.Errorf("%w: credential is for %s, image uses %s", ErrImageCredentialHostMismatch, credentialHost, imageHost)
		}
	}
	for _, reference := range snapshot.SecretReferences {
		var dependencyProjectID string
		err := transaction.QueryRowContext(ctx, "SELECT project_id FROM secrets WHERE id = ?", reference.SecretID).Scan(&dependencyProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: secret %s", ErrDependencyMissing, reference.SecretID)
		}
		if err != nil {
			return fmt.Errorf("load secret dependency: %w", err)
		}
		if dependencyProjectID != projectID {
			return fmt.Errorf("%w: secret %s", ErrDependencyMissing, reference.SecretID)
		}
	}
	for _, mount := range snapshot.VolumeMounts {
		var dependencyProjectID string
		var dependencyServiceID string
		err := transaction.QueryRowContext(ctx, `
SELECT project_id, service_id FROM volumes WHERE id = ?`, mount.VolumeID).Scan(&dependencyProjectID, &dependencyServiceID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: volume %s", ErrDependencyMissing, mount.VolumeID)
		}
		if err != nil {
			return fmt.Errorf("load volume dependency: %w", err)
		}
		if dependencyProjectID != projectID || dependencyServiceID != serviceID {
			return fmt.Errorf("%w: volume %s", ErrDependencyMissing, mount.VolumeID)
		}
	}
	return nil
}

func replaceServiceConfig(ctx context.Context, transaction *sql.Tx, serviceID, projectID string, snapshot serviceconfig.Snapshot, enabled bool, expectedUpdated, updatedAt int64) error {
	commandJSON, err := optionalStringSliceJSON(snapshot.Command)
	if err != nil {
		return err
	}
	argsJSON, err := optionalStringSliceJSON(snapshot.Args)
	if err != nil {
		return err
	}
	environmentJSON, err := json.Marshal(snapshot.Environment)
	if err != nil {
		return fmt.Errorf("encode service environment: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE services SET
  image_reference = ?, image_credential_id = ?, command_json = ?, args_json = ?,
  environment_json = ?, target_port = ?, health_path = ?, startup_timeout_seconds = ?,
  cpu_millis = ?, memory_bytes = ?, enabled = ?,
  active_deployment_id = CASE WHEN ? = 0 THEN NULL ELSE active_deployment_id END,
  updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
		snapshot.ImageReference, nullableString(snapshot.ImageCredentialID), commandJSON, argsJSON,
		string(environmentJSON), nullableInt(snapshot.TargetPort), nullableString(snapshot.HealthPath), snapshot.StartupTimeoutSeconds,
		nullablePositive(snapshot.CPUMillicores), nullablePositive(snapshot.MemoryMaxBytes), boolInteger(enabled),
		boolInteger(enabled), updatedAt, serviceID, projectID, expectedUpdated,
	)
	if err != nil {
		return fmt.Errorf("update service config: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count service update: %w", err)
	}
	if changed != 1 {
		return ErrServiceChanged
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM service_secret_refs WHERE service_id = ?", serviceID); err != nil {
		return fmt.Errorf("clear service secret references: %w", err)
	}
	for _, reference := range snapshot.SecretReferences {
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_secret_refs(service_id, environment_name, secret_id) VALUES (?, ?, ?)`,
			serviceID, reference.EnvironmentName, reference.SecretID,
		); err != nil {
			return fmt.Errorf("replace service secret reference: %w", err)
		}
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM service_volume_mounts WHERE service_id = ?", serviceID); err != nil {
		return fmt.Errorf("clear service volume mounts: %w", err)
	}
	for _, mount := range snapshot.VolumeMounts {
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_volume_mounts(service_id, volume_id, container_path) VALUES (?, ?, ?)`,
			serviceID, mount.VolumeID, mount.ContainerPath,
		); err != nil {
			return fmt.Errorf("replace service volume mount: %w", err)
		}
	}
	return nil
}

type serviceAudit struct {
	ID              string
	ActorID         string
	ActorEmail      string
	Action          string
	ServiceID       string
	CorrelationID   string
	CreatedAtMillis int64
	Metadata        map[string]string
}

func insertServiceAudit(ctx context.Context, transaction *sql.Tx, audit serviceAudit) error {
	metadata := map[string]string{"actorEmail": audit.ActorEmail}
	for key, value := range audit.Metadata {
		metadata[key] = value
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var correlationID any
	if audit.CorrelationID != "" {
		correlationID = audit.CorrelationID
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, 'service', ?, ?, 'succeeded', ?, ?)`,
		audit.ID, audit.ActorID, audit.Action, audit.ServiceID, correlationID, string(encoded), audit.CreatedAtMillis,
	); err != nil {
		return fmt.Errorf("audit %s: %w", audit.Action, err)
	}
	return nil
}

func monotonicTimestamp(expected, current int64) int64 {
	if current <= expected {
		return expected + 1
	}
	return current
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePositive(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
