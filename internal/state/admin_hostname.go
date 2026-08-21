package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

type SetAdminHostnameInput struct {
	Hostname             string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetAdminHostname(ctx context.Context, input SetAdminHostnameInput) error {
	if input.AuditEventID == "" || input.ActorID == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("set admin hostname input is incomplete")
	}
	hostname, err := publichostname.Normalize(input.Hostname)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID, previous string
		if err := transaction.QueryRowContext(ctx, `
SELECT id, admin_hostname FROM installation WHERE singleton = 1`).Scan(&installationID, &previous); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		if hostname == previous {
			return nil
		}
		var inUse int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM service_domains WHERE hostname = ?
  UNION ALL SELECT 1 FROM preview_deployments WHERE hostname = ? AND status = 'active'
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
  UNION ALL SELECT 1 FROM services WHERE sentry_public_hostname = ?
  UNION ALL SELECT 1 FROM services WHERE otlp_trace_public_hostname = ?
)`, hostname, hostname, hostname, hostname, hostname).Scan(&inUse); err != nil {
			return fmt.Errorf("check admin hostname role: %w", err)
		}
		if inUse == 1 {
			return ErrHostnameInUse
		}
		covered, err := originCertificateCoversHostname(ctx, transaction, hostname)
		if err != nil {
			return err
		}
		if !covered {
			return &OriginCertificateCoverageError{Hostnames: []string{hostname}}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation SET admin_hostname = ?, updated_at = ? WHERE singleton = 1`,
			hostname, input.UpdatedAtMillis); err != nil {
			return fmt.Errorf("set admin hostname: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "hostname": hostname, "previousHostname": previous,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'installation.admin_hostname.set', 'installation', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, installationID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
}
