package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/iivankin/platformd/internal/publichostname"
)

type UpdateServiceSentryPublicAccess struct {
	ID                    string
	ProjectID             string
	PublicHostname        string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

var (
	ErrServiceTelemetryTunnelPathInvalid  = errors.New("browser tunnel path is invalid")
	ErrServiceTelemetryTunnelNeedsDomain  = errors.New("browser tunnel requires a public telemetry domain")
	ErrServiceTelemetryPublicPathConflict = errors.New("browser tunnel conflicts with public OTLP endpoint")
)

type UpdateServiceTelemetryTunnel struct {
	ID                    string
	ProjectID             string
	Path                  string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

func (store *Store) UpdateServiceSentryPublicAccess(ctx context.Context, input UpdateServiceSentryPublicAccess) (ServiceDesired, error) {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 || input.AuditEventID == "" ||
		input.UpdatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ServiceDesired{}, errors.New("update service Sentry public access input is incomplete")
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ServiceDesired{}, err
		}
		input.PublicHostname = hostname
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if input.PublicHostname != "" {
			inUse, err := publicHostnameRoleExistsExceptServiceTelemetry(ctx, transaction, input.PublicHostname, input.ID)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
		}
		routes, err := loadServiceTelemetryPublicRoutes(ctx, transaction, input.ProjectID, input.ID)
		if err != nil {
			return err
		}
		if routes.updatedAtMillis != input.ExpectedUpdatedMillis {
			return ErrServiceChanged
		}
		routes.sentryHostname = input.PublicHostname
		if input.PublicHostname == "" {
			routes.sentryTunnelPath = ""
		}
		if routes.pathsConflict() {
			return ErrServiceTelemetryPublicPathConflict
		}
		updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
		hostname := nullableString(input.PublicHostname)
		result, err := transaction.ExecContext(ctx, `
UPDATE services
SET sentry_public_hostname = ?,
    sentry_tunnel_path = CASE WHEN ? IS NULL THEN NULL ELSE sentry_tunnel_path END,
    updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			hostname, hostname, updatedAt, input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("update service Sentry public access: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		metadata, err := json.Marshal(map[string]string{"hostname": input.PublicHostname, "actorEmail": input.ActorEmail})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'service.sentry.public_access.update', 'service', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ProjectID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadata), updatedAt,
		)
		return err
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.Service(ctx, input.ProjectID, input.ID)
}

func (store *Store) ServiceBySentryHostname(ctx context.Context, hostname string) (ServiceDesired, error) {
	var serviceID string
	err := store.database.QueryRowContext(ctx,
		"SELECT id FROM services WHERE sentry_public_hostname = ?", hostname,
	).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDesired{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("load service by Sentry hostname: %w", err)
	}
	return store.DesiredService(ctx, serviceID)
}

func (store *Store) UpdateServiceTelemetryTunnel(ctx context.Context, input UpdateServiceTelemetryTunnel) (ServiceDesired, error) {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 || input.AuditEventID == "" ||
		input.UpdatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ServiceDesired{}, errors.New("update service telemetry tunnel input is incomplete")
	}
	normalized, err := normalizeServiceTelemetryTunnelPath(input.Path)
	if err != nil {
		return ServiceDesired{}, err
	}
	input.Path = normalized
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		routes, err := loadServiceTelemetryPublicRoutes(ctx, transaction, input.ProjectID, input.ID)
		if err != nil {
			return err
		}
		if routes.updatedAtMillis != input.ExpectedUpdatedMillis {
			return ErrServiceChanged
		}
		if routes.sentryHostname == "" {
			return ErrServiceTelemetryTunnelNeedsDomain
		}
		routes.sentryTunnelPath = input.Path
		if routes.pathsConflict() {
			return ErrServiceTelemetryPublicPathConflict
		}
		updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
		result, err := transaction.ExecContext(ctx, `
UPDATE services SET sentry_tunnel_path = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			nullableString(input.Path), updatedAt, input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("update service telemetry tunnel: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		metadata, err := json.Marshal(map[string]string{"path": input.Path, "actorEmail": input.ActorEmail})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'service.telemetry.tunnel.update', 'service', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ProjectID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadata), updatedAt,
		)
		return err
	})
	if err != nil {
		return ServiceDesired{}, err
	}
	return store.Service(ctx, input.ProjectID, input.ID)
}

func normalizeServiceTelemetryTunnelPath(value string) (string, error) {
	return normalizeServiceTelemetryPublicPath(value, ErrServiceTelemetryTunnelPathInvalid)
}

func normalizeServiceTelemetryPublicPath(value string, invalid error) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 256 || value == "/" || !strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return "", invalid
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != value || parsed.RawPath != "" {
		return "", invalid
	}
	return value, nil
}

type serviceTelemetryPublicRoutes struct {
	sentryHostname   string
	sentryTunnelPath string
	otlpHostname     string
	otlpPathPrefix   string
	updatedAtMillis  int64
}

func loadServiceTelemetryPublicRoutes(
	ctx context.Context,
	transaction *sql.Tx,
	projectID string,
	serviceID string,
) (serviceTelemetryPublicRoutes, error) {
	var routes serviceTelemetryPublicRoutes
	var sentryHostname, sentryTunnelPath, otlpHostname, otlpPathPrefix sql.NullString
	err := transaction.QueryRowContext(ctx, `
SELECT sentry_public_hostname, sentry_tunnel_path,
       otlp_trace_public_hostname, otlp_trace_path, updated_at
FROM services
WHERE id = ? AND project_id = ?`, serviceID, projectID).Scan(
		&sentryHostname,
		&sentryTunnelPath,
		&otlpHostname,
		&otlpPathPrefix,
		&routes.updatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return serviceTelemetryPublicRoutes{}, ErrServiceNotFound
	}
	if err != nil {
		return serviceTelemetryPublicRoutes{}, fmt.Errorf("load service telemetry public routes: %w", err)
	}
	routes.sentryHostname = sentryHostname.String
	routes.sentryTunnelPath = sentryTunnelPath.String
	routes.otlpHostname = otlpHostname.String
	routes.otlpPathPrefix = otlpPathPrefix.String
	return routes, nil
}

func (routes serviceTelemetryPublicRoutes) pathsConflict() bool {
	if routes.sentryHostname == "" || routes.sentryHostname != routes.otlpHostname {
		return false
	}
	return routes.sentryTunnelPath == routes.otlpPathPrefix+"/v1/traces" ||
		routes.sentryTunnelPath == routes.otlpPathPrefix+"/v1/logs"
}

func publicHostnameRoleExistsExceptServiceTelemetry(ctx context.Context, transaction *sql.Tx, hostname, serviceID string) (bool, error) {
	var exists int
	err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM installation WHERE admin_hostname = ?
  UNION ALL SELECT 1 FROM service_domains WHERE hostname = ? AND service_id != ?
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
  UNION ALL SELECT 1 FROM services WHERE sentry_public_hostname = ? AND id != ?
  UNION ALL SELECT 1 FROM services WHERE otlp_trace_public_hostname = ? AND id != ?
)`, hostname, hostname, serviceID, hostname, hostname, serviceID, hostname, serviceID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check public hostname roles: %w", err)
	}
	return exists == 1, nil
}
