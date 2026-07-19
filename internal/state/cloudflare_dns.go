package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrCloudflareDNSNotConfigured = errors.New("Cloudflare DNS is not configured")

type CloudflareDNSSettings struct {
	APITokenEncrypted []byte
	CreatedAtMillis   int64
	UpdatedAtMillis   int64
}

func (store *Store) CloudflareDNSSettings(ctx context.Context) (CloudflareDNSSettings, error) {
	var settings CloudflareDNSSettings
	err := store.database.QueryRowContext(ctx, `
SELECT api_token_encrypted, created_at, updated_at
FROM cloudflare_dns_settings WHERE singleton = 1`).Scan(
		&settings.APITokenEncrypted, &settings.CreatedAtMillis, &settings.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CloudflareDNSSettings{}, ErrCloudflareDNSNotConfigured
	}
	if err != nil {
		return CloudflareDNSSettings{}, fmt.Errorf("load Cloudflare DNS settings: %w", err)
	}
	return settings, nil
}

type PutCloudflareDNSSettingsInput struct {
	Settings        CloudflareDNSSettings
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func (store *Store) PutCloudflareDNSSettings(ctx context.Context, input PutCloudflareDNSSettingsInput) error {
	if len(input.Settings.APITokenEncrypted) == 0 || input.AuditEventID == "" || input.ActorID == "" ||
		input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("Cloudflare DNS settings input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var createdAt int64
		err := transaction.QueryRowContext(ctx, "SELECT created_at FROM cloudflare_dns_settings WHERE singleton = 1").Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return fmt.Errorf("load Cloudflare DNS settings creation time: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO cloudflare_dns_settings(singleton, api_token_encrypted, created_at, updated_at)
VALUES (1, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
  api_token_encrypted = excluded.api_token_encrypted,
  updated_at = excluded.updated_at`,
			input.Settings.APITokenEncrypted, createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("save Cloudflare DNS settings: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'cloudflare_dns.configure', 'cloudflare_dns', 'singleton', ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, nullableString(input.CorrelationID), string(metadata), input.UpdatedAtMillis,
		)
		return err
	})
}
