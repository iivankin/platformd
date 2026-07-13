package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/resourcename"
)

var ErrAPITokenNotFound = errors.New("API token not found")

type APIToken struct {
	ID              string
	Name            string
	Role            string
	ProjectID       *string
	SecretHMAC      []byte
	CreatedAtMillis int64
	LastUsedMillis  *int64
	RevokedAtMillis *int64
}

type CreateAPIToken struct {
	APIToken
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
}

type RevokeAPIToken struct {
	ID                   string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	RevokedAtMillis      int64
}

func (store *Store) APITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, role, project_id, created_at, last_used_at, revoked_at
FROM api_tokens ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	defer rows.Close()
	result := make([]APIToken, 0)
	for rows.Next() {
		token, err := scanAPIToken(rows, false)
		if err != nil {
			return nil, err
		}
		result = append(result, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API tokens: %w", err)
	}
	return result, nil
}

func (store *Store) APITokenCredential(ctx context.Context, tokenID string) (APIToken, error) {
	token, err := scanAPIToken(store.database.QueryRowContext(ctx, `
SELECT id, name, role, project_id, secret_hmac, created_at, last_used_at, revoked_at
FROM api_tokens WHERE id = ?`, tokenID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrAPITokenNotFound
	}
	return token, err
}

func (store *Store) CreateAPIToken(ctx context.Context, input CreateAPIToken) (APIToken, error) {
	token := input.APIToken
	if token.ID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || token.CreatedAtMillis <= 0 || len(token.SecretHMAC) != sha256.Size {
		return APIToken{}, errors.New("create API token input is incomplete")
	}
	if err := resourcename.Validate(token.Name); err != nil {
		return APIToken{}, err
	}
	if token.Role != "read" && token.Role != "admin" {
		return APIToken{}, errors.New("API token role must be read or admin")
	}
	metadata := map[string]string{"actorEmail": input.ActorEmail, "role": token.Role}
	if token.ProjectID != nil {
		if *token.ProjectID == "" {
			return APIToken{}, errors.New("API token project ID is empty")
		}
		metadata["projectId"] = *token.ProjectID
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return APIToken{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if token.ProjectID != nil {
			var exists int
			if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", *token.ProjectID).Scan(&exists); err != nil {
				return fmt.Errorf("check API token project: %w", err)
			}
			if exists != 1 {
				return ErrProjectNotFound
			}
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO api_tokens(id, name, role, project_id, secret_hmac, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, token.ID, token.Name, token.Role, token.ProjectID, token.SecretHMAC, token.CreatedAtMillis); err != nil {
			return fmt.Errorf("create API token: %w", err)
		}
		return insertAPITokenAudit(ctx, transaction, input.AuditEventID, input.ActorID, "api_token.create", token.ID, input.RequestCorrelationID, encoded, token.CreatedAtMillis)
	})
	if err != nil {
		return APIToken{}, err
	}
	token.SecretHMAC = nil
	return token, nil
}

func (store *Store) RevokeAPIToken(ctx context.Context, input RevokeAPIToken) error {
	if input.ID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.RevokedAtMillis <= 0 {
		return errors.New("revoke API token input is incomplete")
	}
	encoded, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, input.RevokedAtMillis, input.ID)
		if err != nil {
			return fmt.Errorf("revoke API token: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count revoked API token: %w", err)
		}
		if changed != 1 {
			return ErrAPITokenNotFound
		}
		return insertAPITokenAudit(ctx, transaction, input.AuditEventID, input.ActorID, "api_token.revoke", input.ID, input.RequestCorrelationID, encoded, input.RevokedAtMillis)
	})
}

func scanAPIToken(scanner interface{ Scan(...any) error }, includeSecret bool) (APIToken, error) {
	var token APIToken
	var projectID sql.NullString
	var lastUsed, revokedAt sql.NullInt64
	destinations := []any{&token.ID, &token.Name, &token.Role, &projectID}
	if includeSecret {
		destinations = append(destinations, &token.SecretHMAC)
	}
	destinations = append(destinations, &token.CreatedAtMillis, &lastUsed, &revokedAt)
	if err := scanner.Scan(destinations...); err != nil {
		return APIToken{}, fmt.Errorf("scan API token: %w", err)
	}
	if projectID.Valid {
		token.ProjectID = &projectID.String
	}
	if lastUsed.Valid {
		token.LastUsedMillis = &lastUsed.Int64
	}
	if revokedAt.Valid {
		token.RevokedAtMillis = &revokedAt.Int64
	}
	return token, nil
}

func insertAPITokenAudit(ctx context.Context, transaction *sql.Tx, id, actorID, action, targetID, correlationID string, metadata []byte, timestamp int64) error {
	var requestID any
	if correlationID != "" {
		requestID = correlationID
	}
	_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, 'api_token', ?, ?, 'succeeded', ?, ?)`,
		id, actorID, action, targetID, requestID, string(metadata), timestamp,
	)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
