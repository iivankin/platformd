package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrServiceTelemetryWebhookInvalid  = errors.New("service telemetry webhook is invalid")
	ErrServiceTelemetryWebhookNotFound = errors.New("service telemetry webhook not found")
)

type ServiceTelemetryWebhook struct {
	ID              string
	ServiceID       string
	URL             string
	EventTypes      []string
	SecretEncrypted []byte
	Enabled         bool
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

func (store *Store) ServiceArtifactTokenHash(ctx context.Context, serviceID string) ([]byte, error) {
	var hash []byte
	err := store.database.QueryRowContext(ctx,
		"SELECT artifact_token_sha256 FROM service_telemetry_credentials WHERE service_id = ?", serviceID,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load service artifact token hash: %w", err)
	}
	return hash, nil
}

func (store *Store) SetServiceArtifactTokenHash(ctx context.Context, serviceID string, hash []byte, updatedAtMillis int64) error {
	if serviceID == "" || len(hash) != 32 || updatedAtMillis <= 0 {
		return errors.New("service artifact token input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM services WHERE id = ?)", serviceID).Scan(&exists); err != nil {
			return fmt.Errorf("check artifact token service: %w", err)
		}
		if exists != 1 {
			return ErrServiceNotFound
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO service_telemetry_credentials(service_id, artifact_token_sha256, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(service_id) DO UPDATE SET
  artifact_token_sha256 = excluded.artifact_token_sha256,
  updated_at = excluded.updated_at`, serviceID, hash, updatedAtMillis)
		if err != nil {
			return fmt.Errorf("store service artifact token hash: %w", err)
		}
		return nil
	})
}

func (store *Store) ServiceTelemetryWebhooks(ctx context.Context, serviceID string) ([]ServiceTelemetryWebhook, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, service_id, url, event_types_json, secret_encrypted, enabled, created_at, updated_at
FROM service_telemetry_webhooks
WHERE service_id = ?
ORDER BY created_at, id`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service telemetry webhooks: %w", err)
	}
	defer rows.Close()
	result := make([]ServiceTelemetryWebhook, 0)
	for rows.Next() {
		webhook, scanErr := scanServiceTelemetryWebhook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, webhook)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service telemetry webhooks: %w", err)
	}
	return result, nil
}

func (store *Store) CreateServiceTelemetryWebhook(ctx context.Context, webhook ServiceTelemetryWebhook) error {
	if err := validateServiceTelemetryWebhook(webhook, true); err != nil {
		return err
	}
	events, err := json.Marshal(webhook.EventTypes)
	if err != nil {
		return fmt.Errorf("encode service telemetry webhook events: %w", err)
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM services WHERE id = ?)", webhook.ServiceID).Scan(&exists); err != nil {
			return fmt.Errorf("check telemetry webhook service: %w", err)
		}
		if exists != 1 {
			return ErrServiceNotFound
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO service_telemetry_webhooks(
  id, service_id, url, event_types_json, secret_encrypted, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, webhook.ID, webhook.ServiceID, webhook.URL, string(events), webhook.SecretEncrypted,
			webhook.Enabled, webhook.CreatedAtMillis, webhook.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create service telemetry webhook: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateServiceTelemetryWebhook(ctx context.Context, webhook ServiceTelemetryWebhook) error {
	if err := validateServiceTelemetryWebhook(webhook, false); err != nil {
		return err
	}
	events, err := json.Marshal(webhook.EventTypes)
	if err != nil {
		return fmt.Errorf("encode service telemetry webhook events: %w", err)
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE service_telemetry_webhooks
SET url = ?, event_types_json = ?, enabled = ?, updated_at = ?
WHERE id = ? AND service_id = ?`, webhook.URL, string(events), webhook.Enabled, webhook.UpdatedAtMillis,
			webhook.ID, webhook.ServiceID)
		if err != nil {
			return fmt.Errorf("update service telemetry webhook: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrServiceTelemetryWebhookNotFound
		}
		return nil
	})
}

func (store *Store) DeleteServiceTelemetryWebhook(ctx context.Context, serviceID, webhookID string) error {
	if serviceID == "" || webhookID == "" {
		return errors.New("delete service telemetry webhook input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx,
			"DELETE FROM service_telemetry_webhooks WHERE id = ? AND service_id = ?", webhookID, serviceID,
		)
		if err != nil {
			return fmt.Errorf("delete service telemetry webhook: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrServiceTelemetryWebhookNotFound
		}
		return nil
	})
}

func validateServiceTelemetryWebhook(webhook ServiceTelemetryWebhook, creating bool) error {
	if webhook.ID == "" || webhook.ServiceID == "" || webhook.URL == "" || len(webhook.EventTypes) == 0 ||
		webhook.UpdatedAtMillis <= 0 || (creating && (len(webhook.SecretEncrypted) == 0 || webhook.CreatedAtMillis != webhook.UpdatedAtMillis)) {
		return ErrServiceTelemetryWebhookInvalid
	}
	return nil
}

func scanServiceTelemetryWebhook(scanner interface{ Scan(...any) error }) (ServiceTelemetryWebhook, error) {
	var webhook ServiceTelemetryWebhook
	var events string
	if err := scanner.Scan(&webhook.ID, &webhook.ServiceID, &webhook.URL, &events, &webhook.SecretEncrypted,
		&webhook.Enabled, &webhook.CreatedAtMillis, &webhook.UpdatedAtMillis); err != nil {
		return ServiceTelemetryWebhook{}, fmt.Errorf("scan service telemetry webhook: %w", err)
	}
	if err := json.Unmarshal([]byte(events), &webhook.EventTypes); err != nil {
		return ServiceTelemetryWebhook{}, fmt.Errorf("decode service telemetry webhook events: %w", err)
	}
	return webhook, nil
}
