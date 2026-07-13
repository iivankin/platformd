package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/resourcename"
)

var ErrRegistryCredentialNameConflict = errors.New("registry credential name already exists")

type CreateRegistryCredential struct {
	ID                   string
	RepositoryID         string
	Name                 string
	Permission           string
	SecretHMAC           []byte
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) CreateRegistryCredential(ctx context.Context, input CreateRegistryCredential) (RegistryCredential, error) {
	if input.ID == "" || input.RepositoryID == "" || len(input.SecretHMAC) != 32 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return RegistryCredential{}, errors.New("create registry credential input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return RegistryCredential{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return RegistryCredential{}, err
	}
	if input.Permission != "pull" && input.Permission != "pull_push" {
		return RegistryCredential{}, errors.New("registry credential permission must be pull or pull_push")
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var repositoryName string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM registry_repositories WHERE id = ?", input.RepositoryID).Scan(&repositoryName); errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryRepositoryNotFound
		} else if err != nil {
			return err
		}
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM registry_credentials WHERE repository_id = ? AND name = ?)`,
			input.RepositoryID, input.Name).Scan(&exists); err != nil {
			return err
		}
		if exists == 1 {
			return ErrRegistryCredentialNameConflict
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_credentials(id, repository_id, name, permission, secret_hmac, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.ID, input.RepositoryID, input.Name,
			input.Permission, input.SecretHMAC, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create registry credential: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "name": input.Name,
			"permission": input.Permission, "repositoryName": repositoryName,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'registry.credential.create', 'registry_credential', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis)
		return err
	})
	if err != nil {
		return RegistryCredential{}, err
	}
	return store.RegistryCredential(ctx, input.ID)
}

func (store *Store) RegistryCredentials(ctx context.Context, repositoryID string) ([]RegistryCredential, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, repository_id, name, permission, secret_hmac, created_at, last_used_at
FROM registry_credentials WHERE repository_id = ? ORDER BY name, id`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RegistryCredential, 0)
	for rows.Next() {
		var credential RegistryCredential
		var lastUsed sql.NullInt64
		if err := rows.Scan(
			&credential.ID, &credential.RepositoryID, &credential.Name, &credential.Permission,
			&credential.SecretHMAC, &credential.CreatedAtMillis, &lastUsed,
		); err != nil {
			return nil, err
		}
		credential.LastUsedAtMillis = lastUsed.Int64
		result = append(result, credential)
	}
	return result, rows.Err()
}

func (store *Store) TouchRegistryCredentialLastUsed(ctx context.Context, credentialID string, usedAtMillis int64) error {
	if credentialID == "" || usedAtMillis <= 0 {
		return errors.New("touch registry credential input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE registry_credentials SET last_used_at = ? WHERE id = ?`, usedAtMillis, credentialID)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrRegistryCredentialNotFound
		}
		return nil
	})
}

type DeleteRegistryCredential struct {
	RepositoryID         string
	CredentialID         string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) DeleteRegistryCredential(ctx context.Context, input DeleteRegistryCredential) ([]RegistryUpload, error) {
	if input.RepositoryID == "" || input.CredentialID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return nil, errors.New("delete registry credential input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return nil, err
	}
	uploads := make([]RegistryUpload, 0)
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var credentialName string
		if err := transaction.QueryRowContext(ctx, `
SELECT name FROM registry_credentials WHERE id = ? AND repository_id = ?`,
			input.CredentialID, input.RepositoryID).Scan(&credentialName); errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryCredentialNotFound
		} else if err != nil {
			return err
		}
		rows, err := transaction.QueryContext(ctx, `
SELECT id, repository_id, credential_id, created_at, updated_at, expires_at
FROM registry_uploads WHERE credential_id = ? ORDER BY id`, input.CredentialID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var upload RegistryUpload
			if err := rows.Scan(
				&upload.ID, &upload.RepositoryID, &upload.CredentialID,
				&upload.CreatedAtMillis, &upload.UpdatedAtMillis, &upload.ExpiresAtMillis,
			); err != nil {
				_ = rows.Close()
				return err
			}
			uploads = append(uploads, upload)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
DELETE FROM registry_credentials WHERE id = ? AND repository_id = ?`, input.CredentialID, input.RepositoryID); err != nil {
			return err
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "name": credentialName,
			"repositoryId": input.RepositoryID,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'registry.credential.delete', 'registry_credential', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.CredentialID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis)
		return err
	})
	return uploads, err
}
