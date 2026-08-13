package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
			inUse, err := publicHostnameRoleExistsExceptServiceSentry(ctx, transaction, input.PublicHostname, input.ID)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
		}
		updatedAt := monotonicTimestamp(input.ExpectedUpdatedMillis, input.UpdatedAtMillis)
		result, err := transaction.ExecContext(ctx, `
UPDATE services SET sentry_public_hostname = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			nullableString(input.PublicHostname), updatedAt, input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
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

func publicHostnameRoleExistsExceptServiceSentry(ctx context.Context, transaction *sql.Tx, hostname, serviceID string) (bool, error) {
	var exists int
	err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM installation WHERE admin_hostname = ?
  UNION ALL SELECT 1 FROM service_domains WHERE hostname = ?
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
  UNION ALL SELECT 1 FROM services WHERE sentry_public_hostname = ? AND id != ?
)`, hostname, hostname, hostname, hostname, serviceID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check public hostname roles: %w", err)
	}
	return exists == 1, nil
}
