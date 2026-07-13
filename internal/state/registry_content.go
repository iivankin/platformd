package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/registryname"
)

var (
	ErrRegistryUploadNotFound   = errors.New("registry upload not found")
	ErrRegistryUploadQuota      = errors.New("registry upload session quota exceeded")
	ErrRegistryManifestNotFound = errors.New("registry manifest not found")
	ErrRegistryManifestQuota    = errors.New("registry manifest quota exceeded")
)

type RegistryUpload struct {
	ID              string
	RepositoryID    string
	CredentialID    string
	CreatedAtMillis int64
	UpdatedAtMillis int64
	ExpiresAtMillis int64
}

type CreateRegistryUpload struct {
	ID                   string
	RepositoryID         string
	CredentialID         string
	CreatedAtMillis      int64
	ExpiresAtMillis      int64
	MaximumForCredential int
}

func (store *Store) CreateRegistryUpload(ctx context.Context, input CreateRegistryUpload) (RegistryUpload, error) {
	if input.ID == "" || input.RepositoryID == "" || input.CredentialID == "" || input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis || input.MaximumForCredential < 1 {
		return RegistryUpload{}, errors.New("create registry upload input is invalid")
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var credentialRepositoryID string
		err := transaction.QueryRowContext(ctx, "SELECT repository_id FROM registry_credentials WHERE id = ?", input.CredentialID).Scan(&credentialRepositoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryCredentialNotFound
		}
		if err != nil {
			return err
		}
		if credentialRepositoryID != input.RepositoryID {
			return ErrRegistryCredentialNotFound
		}
		var uploadCount int
		if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM registry_uploads WHERE credential_id = ? AND expires_at > ?", input.CredentialID, input.CreatedAtMillis).Scan(&uploadCount); err != nil {
			return err
		}
		if uploadCount >= input.MaximumForCredential {
			return ErrRegistryUploadQuota
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO registry_uploads(id, repository_id, credential_id, created_at, updated_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.ID, input.RepositoryID, input.CredentialID,
			input.CreatedAtMillis, input.CreatedAtMillis, input.ExpiresAtMillis)
		return err
	})
	if err != nil {
		return RegistryUpload{}, err
	}
	return store.RegistryUpload(ctx, input.ID)
}

func (store *Store) RegistryUpload(ctx context.Context, uploadID string) (RegistryUpload, error) {
	var result RegistryUpload
	err := store.database.QueryRowContext(ctx, `
SELECT id, repository_id, credential_id, created_at, updated_at, expires_at
FROM registry_uploads WHERE id = ?`, uploadID).Scan(
		&result.ID, &result.RepositoryID, &result.CredentialID,
		&result.CreatedAtMillis, &result.UpdatedAtMillis, &result.ExpiresAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistryUpload{}, ErrRegistryUploadNotFound
	}
	return result, err
}

func (store *Store) TouchRegistryUpload(ctx context.Context, uploadID string, updatedAtMillis int64) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, "UPDATE registry_uploads SET updated_at = ? WHERE id = ? AND expires_at > ?", updatedAtMillis, uploadID, updatedAtMillis)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrRegistryUploadNotFound
		}
		return nil
	})
}

func (store *Store) DeleteRegistryUpload(ctx context.Context, uploadID string) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, "DELETE FROM registry_uploads WHERE id = ?", uploadID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return ErrRegistryUploadNotFound
		}
		return nil
	})
}

func (store *Store) RegistryUploadCountForCredential(ctx context.Context, credentialID string) (int, error) {
	var count int
	err := store.database.QueryRowContext(ctx, "SELECT count(*) FROM registry_uploads WHERE credential_id = ?", credentialID).Scan(&count)
	return count, err
}

func (store *Store) ExpiredRegistryUploads(ctx context.Context, beforeMillis int64, limit int) ([]RegistryUpload, error) {
	if beforeMillis <= 0 || limit < 1 || limit > 1000 {
		return nil, errors.New("expired registry upload query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, repository_id, credential_id, created_at, updated_at, expires_at
FROM registry_uploads WHERE expires_at <= ? ORDER BY expires_at, id LIMIT ?`, beforeMillis, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RegistryUpload, 0, limit)
	for rows.Next() {
		var upload RegistryUpload
		if err := rows.Scan(
			&upload.ID, &upload.RepositoryID, &upload.CredentialID,
			&upload.CreatedAtMillis, &upload.UpdatedAtMillis, &upload.ExpiresAtMillis,
		); err != nil {
			return nil, err
		}
		result = append(result, upload)
	}
	return result, rows.Err()
}

type RegistryManifest struct {
	RepositoryID   string
	Digest         string
	MediaType      string
	Body           []byte
	PushedAtMillis int64
}

type PutRegistryManifest struct {
	RepositoryID         string
	Digest               string
	MediaType            string
	Body                 []byte
	Tag                  string
	PushedAtMillis       int64
	MaximumForRepository int
}

func (store *Store) PutRegistryManifest(ctx context.Context, input PutRegistryManifest) (RegistryManifest, error) {
	if input.RepositoryID == "" || registryname.ValidateDigest(input.Digest) != nil || input.MediaType == "" || len(input.Body) == 0 || input.PushedAtMillis <= 0 || input.MaximumForRepository < 1 {
		return RegistryManifest{}, errors.New("put registry manifest input is invalid")
	}
	if input.Tag != "" {
		if err := registryname.ValidateTag(input.Tag); err != nil {
			return RegistryManifest{}, err
		}
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM registry_repositories WHERE id = ?)", input.RepositoryID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrRegistryRepositoryNotFound
		}
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM registry_manifests WHERE repository_id = ? AND digest = ?)`,
			input.RepositoryID, input.Digest).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			var manifestCount int
			if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM registry_manifests WHERE repository_id = ?", input.RepositoryID).Scan(&manifestCount); err != nil {
				return err
			}
			if manifestCount >= input.MaximumForRepository {
				return ErrRegistryManifestQuota
			}
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_manifests(repository_id, digest, media_type, body, pushed_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(repository_id, digest) DO NOTHING`, input.RepositoryID, input.Digest,
			input.MediaType, input.Body, input.PushedAtMillis); err != nil {
			return fmt.Errorf("publish registry manifest: %w", err)
		}
		if input.Tag != "" {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_tags(repository_id, name, manifest_digest, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(repository_id, name) DO UPDATE SET
  manifest_digest = excluded.manifest_digest, updated_at = excluded.updated_at`,
				input.RepositoryID, input.Tag, input.Digest, input.PushedAtMillis); err != nil {
				return fmt.Errorf("publish registry tag: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return RegistryManifest{}, err
	}
	return store.RegistryManifest(ctx, input.RepositoryID, input.Digest)
}

func (store *Store) RegistryManifest(ctx context.Context, repositoryID, reference string) (RegistryManifest, error) {
	digest := reference
	if registryname.ValidateDigest(reference) != nil {
		if err := registryname.ValidateTag(reference); err != nil {
			return RegistryManifest{}, ErrRegistryManifestNotFound
		}
		if err := store.database.QueryRowContext(ctx, `
SELECT manifest_digest FROM registry_tags WHERE repository_id = ? AND name = ?`,
			repositoryID, reference).Scan(&digest); errors.Is(err, sql.ErrNoRows) {
			return RegistryManifest{}, ErrRegistryManifestNotFound
		} else if err != nil {
			return RegistryManifest{}, err
		}
	}
	var result RegistryManifest
	err := store.database.QueryRowContext(ctx, `
SELECT repository_id, digest, media_type, body, pushed_at
FROM registry_manifests WHERE repository_id = ? AND digest = ?`, repositoryID, digest).Scan(
		&result.RepositoryID, &result.Digest, &result.MediaType, &result.Body, &result.PushedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistryManifest{}, ErrRegistryManifestNotFound
	}
	return result, err
}

func (store *Store) RegistryManifestExists(ctx context.Context, repositoryID, digest string) (bool, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM registry_manifests WHERE repository_id = ? AND digest = ?)", repositoryID, digest).Scan(&exists)
	return exists == 1, err
}

func (store *Store) RegistryManifestCount(ctx context.Context, repositoryID string) (int, error) {
	var count int
	err := store.database.QueryRowContext(ctx, "SELECT count(*) FROM registry_manifests WHERE repository_id = ?", repositoryID).Scan(&count)
	return count, err
}

type RegistryTag struct {
	Name            string
	ManifestDigest  string
	UpdatedAtMillis int64
}

func (store *Store) RegistryTags(ctx context.Context, repositoryID, after string, limit int) ([]RegistryTag, bool, error) {
	if limit < 1 || limit > 1000 {
		return nil, false, errors.New("registry tag list limit must be 1..1000")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT name, manifest_digest, updated_at FROM registry_tags
WHERE repository_id = ? AND name > ? ORDER BY name LIMIT ?`, repositoryID, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]RegistryTag, 0, limit)
	for rows.Next() {
		var tag RegistryTag
		if err := rows.Scan(&tag.Name, &tag.ManifestDigest, &tag.UpdatedAtMillis); err != nil {
			return nil, false, err
		}
		result = append(result, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}
