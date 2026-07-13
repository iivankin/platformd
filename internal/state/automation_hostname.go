package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

type SetAutomationHostnameInput struct {
	Hostname             string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetAutomationHostname(ctx context.Context, input SetAutomationHostnameInput) (*string, error) {
	if input.AuditEventID == "" || input.ActorID == "" || input.UpdatedAtMillis <= 0 {
		return nil, errors.New("set automation hostname input is incomplete")
	}
	hostname := ""
	if input.Hostname != "" {
		normalized, err := publichostname.Normalize(input.Hostname)
		if err != nil {
			return nil, err
		}
		hostname = normalized
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID, adminHostname string
		var previous, registryHostname sql.NullString
		if err := transaction.QueryRowContext(ctx, `
SELECT id, admin_hostname, automation_hostname, registry_hostname
FROM installation WHERE singleton = 1`).Scan(&installationID, &adminHostname, &previous, &registryHostname); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		if hostname != "" {
			if hostname == adminHostname || (registryHostname.Valid && hostname == registryHostname.String) {
				return ErrHostnameInUse
			}
			var inUse int
			if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM service_domains WHERE hostname = ?
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
)`, hostname, hostname).Scan(&inUse); err != nil {
				return err
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
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation SET automation_hostname = ?, updated_at = ? WHERE singleton = 1`,
			nullableString(hostname), input.UpdatedAtMillis); err != nil {
			return fmt.Errorf("set automation hostname: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "hostname": hostname, "previousHostname": previous.String,
		})
		if err != nil {
			return err
		}
		action := "installation.automation_hostname.set"
		if hostname == "" {
			action = "installation.automation_hostname.clear"
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, 'installation', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, action, installationID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	if err != nil {
		return nil, err
	}
	if hostname == "" {
		return nil, nil
	}
	return &hostname, nil
}
