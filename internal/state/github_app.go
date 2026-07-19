package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrGitHubAppNotConfigured = errors.New("GitHub App is not configured")

type GitHubAppSettings struct {
	AppID                  int64
	AppSlug                string
	PrivateKeyEncrypted    []byte
	WebhookSecretEncrypted []byte
	CreatedAtMillis        int64
	UpdatedAtMillis        int64
}

func (store *Store) GitHubAppSettings(ctx context.Context) (GitHubAppSettings, error) {
	var settings GitHubAppSettings
	err := store.database.QueryRowContext(ctx, `
SELECT app_id, app_slug, private_key_encrypted, webhook_secret_encrypted, created_at, updated_at
FROM github_app_settings WHERE singleton = 1`).Scan(
		&settings.AppID, &settings.AppSlug, &settings.PrivateKeyEncrypted,
		&settings.WebhookSecretEncrypted, &settings.CreatedAtMillis, &settings.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubAppSettings{}, ErrGitHubAppNotConfigured
	}
	if err != nil {
		return GitHubAppSettings{}, fmt.Errorf("load GitHub App settings: %w", err)
	}
	return settings, nil
}

type PutGitHubAppSettingsInput struct {
	Settings        GitHubAppSettings
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func (store *Store) PutGitHubAppSettings(ctx context.Context, input PutGitHubAppSettingsInput) error {
	settings := input.Settings
	if settings.AppID <= 0 || settings.AppSlug == "" || len(settings.PrivateKeyEncrypted) == 0 ||
		len(settings.WebhookSecretEncrypted) == 0 || input.AuditEventID == "" || input.ActorID == "" ||
		input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("GitHub App settings input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var createdAt int64
		err := transaction.QueryRowContext(ctx, "SELECT created_at FROM github_app_settings WHERE singleton = 1").Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return fmt.Errorf("load GitHub App creation time: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO github_app_settings(
  singleton, app_id, app_slug, private_key_encrypted, webhook_secret_encrypted, created_at, updated_at
) VALUES (1, ?, ?, ?, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
  app_id = excluded.app_id,
  app_slug = excluded.app_slug,
  private_key_encrypted = excluded.private_key_encrypted,
  webhook_secret_encrypted = excluded.webhook_secret_encrypted,
  updated_at = excluded.updated_at`,
			settings.AppID, settings.AppSlug, settings.PrivateKeyEncrypted,
			settings.WebhookSecretEncrypted, createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("save GitHub App settings: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
		if err != nil {
			return err
		}
		var correlationID any
		if input.CorrelationID != "" {
			correlationID = input.CorrelationID
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'github_app.configure', 'github_app', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, settings.AppSlug, correlationID, string(metadata), input.UpdatedAtMillis,
		)
		return err
	})
}
