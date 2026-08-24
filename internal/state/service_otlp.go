package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

var ErrServiceOTLPPublicAccessInvalid = errors.New("public OTLP endpoint is invalid")

type UpdateServiceOTLPPublicAccess struct {
	ID                    string
	ProjectID             string
	PublicHostname        string
	PathPrefix            string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

func (store *Store) UpdateServiceOTLPPublicAccess(
	ctx context.Context,
	input UpdateServiceOTLPPublicAccess,
) (ServiceDesired, error) {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 || input.AuditEventID == "" ||
		input.UpdatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ServiceDesired{}, errors.New("update public OTLP access input is incomplete")
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ServiceDesired{}, err
		}
		input.PublicHostname = hostname
	}
	pathPrefix, err := normalizeServiceTelemetryPublicPath(input.PathPrefix, ErrServiceOTLPPublicAccessInvalid)
	if err != nil {
		return ServiceDesired{}, err
	}
	input.PathPrefix = pathPrefix
	if (input.PublicHostname == "") != (input.PathPrefix == "") {
		return ServiceDesired{}, ErrServiceOTLPPublicAccessInvalid
	}

	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
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
		routes.otlpHostname = input.PublicHostname
		routes.otlpPathPrefix = input.PathPrefix
		if routes.pathsConflict() {
			return ErrServiceTelemetryPublicPathConflict
		}
		updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
		result, err := transaction.ExecContext(ctx, `
UPDATE services
SET otlp_trace_public_hostname = ?, otlp_trace_path = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			nullableString(input.PublicHostname), nullableString(input.PathPrefix), updatedAt,
			input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("update public OTLP access: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrServiceChanged
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail,
			"hostname":   input.PublicHostname,
			"pathPrefix": input.PathPrefix,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'service.telemetry.otlp_public_access.update', 'service', ?, ?, 'succeeded', ?, ?)`,
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

func (store *Store) ServiceByOTLPHostname(ctx context.Context, hostname string) (ServiceDesired, error) {
	var serviceID string
	err := store.database.QueryRowContext(ctx,
		"SELECT id FROM services WHERE otlp_trace_public_hostname = ?", hostname,
	).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDesired{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("load service by OTLP hostname: %w", err)
	}
	return store.DesiredService(ctx, serviceID)
}
