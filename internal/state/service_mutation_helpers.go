package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
)

func validateServiceMutationIdentity(serviceID, projectID string, expectedUpdated int64, auditID, actorKind, actorID, actorEmail string, timestamp int64) error {
	if serviceID == "" || projectID == "" || expectedUpdated <= 0 || auditID == "" || timestamp <= 0 || validateMutationActor(actorKind, actorID, actorEmail) != nil {
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
	if servicesource.ImageUploadPreviewsEnabled(snapshot.Source) {
		var domainCount int
		if err := transaction.QueryRowContext(ctx, `
SELECT count(*) FROM service_domains WHERE service_id = ?`, serviceID).Scan(&domainCount); err != nil {
			return fmt.Errorf("count image upload preview domains: %w", err)
		}
		if domainCount != 1 {
			return ErrPreviewDomainCount
		}
	}
	if snapshot.Source.Type == servicesource.PrivateImage {
		var credentialHost string
		err := transaction.QueryRowContext(ctx, `
		SELECT registry_host FROM service_image_credentials WHERE service_id = ?`, serviceID).Scan(&credentialHost)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: private registry credential", ErrDependencyMissing)
		}
		if err != nil {
			return fmt.Errorf("load service image credential: %w", err)
		}
		imageHost, err := imagecredential.HostForReference(servicesource.ImageReference(snapshot.Source))
		if err != nil {
			return err
		}
		if imageHost != credentialHost {
			return fmt.Errorf("%w: credential is for %s, image uses %s", ErrImageCredentialHostMismatch, credentialHost, imageHost)
		}
	}
	if snapshot.BeforeDeploy != nil && len(snapshot.BeforeDeploy.CloudflareHostnames) > 0 {
		for _, hostname := range snapshot.BeforeDeploy.CloudflareHostnames {
			var exists int
			if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM service_domains WHERE service_id = ? AND hostname = ?)`, serviceID, hostname).Scan(&exists); err != nil {
				return fmt.Errorf("validate before-deploy Cloudflare hostname: %w", err)
			}
			if exists != 1 {
				return fmt.Errorf("%w: before-deploy Cloudflare hostname %s", ErrDependencyMissing, hostname)
			}
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
	beforeDeployJSON, err := optionalJSON(snapshot.BeforeDeploy)
	if err != nil {
		return fmt.Errorf("encode service before-deploy settings: %w", err)
	}
	portForwardJSON, err := optionalJSON(snapshot.PortForward)
	if err != nil {
		return fmt.Errorf("encode service port-forward settings: %w", err)
	}
	sourceJSON, err := json.Marshal(snapshot.Source)
	if err != nil {
		return fmt.Errorf("encode service source: %w", err)
	}
	var healthPort any
	var healthPath any
	healthTimeout := serviceconfig.DefaultHealthTimeoutSeconds
	if snapshot.HealthCheck != nil {
		healthPort = snapshot.HealthCheck.Port
		healthPath = snapshot.HealthCheck.Path
		healthTimeout = snapshot.HealthCheck.TimeoutSeconds
	}
	result, err := transaction.ExecContext(ctx, `
	UPDATE services SET
	  source_json = ?, command_json = ?, args_json = ?,
	  environment_json = ?, before_deploy_json = ?, port_forward_json = ?, health_port = ?, health_path = ?, health_timeout_seconds = ?,
  cpu_millis = ?, memory_bytes = ?, enabled = ?,
  active_deployment_id = CASE WHEN ? = 0 THEN NULL ELSE active_deployment_id END,
  updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
		string(sourceJSON), commandJSON, argsJSON,
		string(environmentJSON), beforeDeployJSON, portForwardJSON, healthPort, healthPath, healthTimeout,
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
	ProjectID       string
	ActorKind       string
	ActorID         string
	ActorEmail      string
	Action          string
	ServiceID       string
	CorrelationID   string
	CreatedAtMillis int64
	Metadata        map[string]string
}

func insertServiceAudit(ctx context.Context, transaction *sql.Tx, audit serviceAudit) error {
	if audit.ProjectID == "" {
		return errors.New("service audit project ID is empty")
	}
	if err := validateMutationActor(audit.ActorKind, audit.ActorID, audit.ActorEmail); err != nil {
		return err
	}
	metadata := make(map[string]string)
	if audit.ActorEmail != "" {
		metadata["actorEmail"] = audit.ActorEmail
	}
	for key, value := range audit.Metadata {
		metadata[key] = value
	}
	if metadata["name"] == "" {
		var name string
		if err := transaction.QueryRowContext(ctx, `
SELECT name FROM services WHERE id = ? AND project_id = ?`, audit.ServiceID, audit.ProjectID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
			return ErrServiceNotFound
		} else if err != nil {
			return fmt.Errorf("load service audit target: %w", err)
		}
		metadata["name"] = name
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
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, 'service', ?, ?, 'succeeded', ?, ?)`,
		audit.ID, audit.ProjectID, audit.ActorKind, audit.ActorID, audit.Action, audit.ServiceID, correlationID, string(encoded), audit.CreatedAtMillis,
	); err != nil {
		return fmt.Errorf("audit %s: %w", audit.Action, err)
	}
	return nil
}

func validateMutationActor(kind, id, email string) error {
	if id == "" {
		return errors.New("mutation actor ID is empty")
	}
	switch kind {
	case "access":
		if email == "" {
			return errors.New("Access mutation actor email is empty")
		}
	case "token":
		if email != "" {
			return errors.New("token mutation actor must not carry an Access email")
		}
	default:
		return errors.New("mutation actor kind must be access or token")
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
