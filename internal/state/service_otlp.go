package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

var ErrServiceOTLPTracePublicAccessInvalid = errors.New("public OTLP trace endpoint is invalid")

type UpdateServiceOTLPTracePublicAccess struct {
	ID                    string
	ProjectID             string
	PublicHostname        string
	Path                  string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	UpdatedAtMillis       int64
}

func (store *Store) UpdateServiceOTLPTracePublicAccess(
	ctx context.Context,
	input UpdateServiceOTLPTracePublicAccess,
) (ServiceDesired, error) {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 || input.AuditEventID == "" ||
		input.UpdatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ServiceDesired{}, errors.New("update public OTLP trace access input is incomplete")
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ServiceDesired{}, err
		}
		input.PublicHostname = hostname
	}
	tracePath, err := normalizeServiceTelemetryPublicPath(input.Path, ErrServiceOTLPTracePublicAccessInvalid)
	if err != nil {
		return ServiceDesired{}, err
	}
	input.Path = tracePath
	if (input.PublicHostname == "") != (input.Path == "") {
		return ServiceDesired{}, ErrServiceOTLPTracePublicAccessInvalid
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
		updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
		result, err := transaction.ExecContext(ctx, `
UPDATE services
SET otlp_trace_public_hostname = ?, otlp_trace_path = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			nullableString(input.PublicHostname), nullableString(input.Path), updatedAt,
			input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("update public OTLP trace access: %w", err)
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
			"path":       input.Path,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'service.telemetry.otlp_trace_public_access.update', 'service', ?, ?, 'succeeded', ?, ?)`,
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

func (store *Store) ServiceByOTLPTraceHostname(ctx context.Context, hostname string) (ServiceDesired, error) {
	var serviceID string
	err := store.database.QueryRowContext(ctx,
		"SELECT id FROM services WHERE otlp_trace_public_hostname = ?", hostname,
	).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDesired{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceDesired{}, fmt.Errorf("load service by OTLP trace hostname: %w", err)
	}
	return store.DesiredService(ctx, serviceID)
}
