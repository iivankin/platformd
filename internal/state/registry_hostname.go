package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

type SetRegistryHostnameInput struct {
	Hostname             string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetRegistryHostname(ctx context.Context, input SetRegistryHostnameInput) (*string, error) {
	if input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return nil, errors.New("set registry hostname input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return nil, err
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
		var installationID string
		var previous sql.NullString
		if err := transaction.QueryRowContext(ctx, "SELECT id, registry_hostname FROM installation WHERE singleton = 1").Scan(&installationID, &previous); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		if hostname != "" {
			var inUse int
			if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM installation WHERE admin_hostname = ? OR automation_hostname = ?
  UNION ALL SELECT 1 FROM service_domains WHERE hostname = ?
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
)`, hostname, hostname, hostname, hostname).Scan(&inUse); err != nil {
				return fmt.Errorf("check registry hostname role: %w", err)
			}
			if inUse == 1 {
				return ErrHostnameInUse
			}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation SET registry_hostname = ?, updated_at = ? WHERE singleton = 1`,
			nullableString(hostname), input.UpdatedAtMillis); err != nil {
			return fmt.Errorf("set registry hostname: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "hostname": hostname, "previousHostname": previous.String,
		})
		if err != nil {
			return err
		}
		action := "registry.hostname.set"
		if hostname == "" {
			action = "registry.hostname.clear"
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'installation', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, action, installationID,
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
