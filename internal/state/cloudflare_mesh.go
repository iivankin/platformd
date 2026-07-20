package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrCloudflareMeshNotConfigured = errors.New("Cloudflare Mesh is not configured")

type CloudflareMeshSettings struct {
	AccountID         string
	APITokenEncrypted []byte
	NodeID            string
	NodeName          string
	CreatedAtMillis   int64
	UpdatedAtMillis   int64
}

func (store *Store) CloudflareMeshSettings(ctx context.Context) (CloudflareMeshSettings, error) {
	var settings CloudflareMeshSettings
	err := store.database.QueryRowContext(ctx, `
SELECT account_id, api_token_encrypted, node_id, node_name, created_at, updated_at
FROM cloudflare_mesh_settings WHERE singleton = 1`).Scan(
		&settings.AccountID, &settings.APITokenEncrypted, &settings.NodeID, &settings.NodeName,
		&settings.CreatedAtMillis, &settings.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CloudflareMeshSettings{}, ErrCloudflareMeshNotConfigured
	}
	if err != nil {
		return CloudflareMeshSettings{}, fmt.Errorf("load Cloudflare Mesh settings: %w", err)
	}
	return settings, nil
}

type PutCloudflareMeshSettingsInput struct {
	Settings        CloudflareMeshSettings
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func (store *Store) PutCloudflareMeshSettings(ctx context.Context, input PutCloudflareMeshSettingsInput) error {
	settings := input.Settings
	if settings.AccountID == "" || len(settings.APITokenEncrypted) == 0 || settings.NodeID == "" ||
		settings.NodeName == "" || input.AuditEventID == "" || input.ActorID == "" ||
		input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("Cloudflare Mesh settings input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var createdAt int64
		err := transaction.QueryRowContext(ctx, "SELECT created_at FROM cloudflare_mesh_settings WHERE singleton = 1").Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return fmt.Errorf("load Cloudflare Mesh settings creation time: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO cloudflare_mesh_settings(
  singleton, account_id, api_token_encrypted, node_id, node_name, created_at, updated_at
) VALUES (1, ?, ?, ?, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
  account_id = excluded.account_id,
  api_token_encrypted = excluded.api_token_encrypted,
  node_id = excluded.node_id,
  node_name = excluded.node_name,
  updated_at = excluded.updated_at`,
			settings.AccountID, settings.APITokenEncrypted, settings.NodeID, settings.NodeName,
			createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("save Cloudflare Mesh settings: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"accountId": settings.AccountID, "actorEmail": input.ActorEmail,
			"nodeId": settings.NodeID, "nodeName": settings.NodeName,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'cloudflare_mesh.configure', 'cloudflare_mesh', 'singleton', ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, nullableString(input.CorrelationID), string(metadata), input.UpdatedAtMillis,
		)
		return err
	})
}
