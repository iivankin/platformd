package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type RegistryRepositoryMetadataStats struct {
	ManifestCount      int
	TagCount           int
	LastPushedAtMillis int64
}

func (store *Store) RegistryRepositoryMetadataStats(ctx context.Context, repositoryID string) (RegistryRepositoryMetadataStats, error) {
	var result RegistryRepositoryMetadataStats
	var lastPushed sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM registry_manifests WHERE repository_id = ?),
  (SELECT count(*) FROM registry_tags WHERE repository_id = ?),
  (SELECT max(pushed_at) FROM registry_manifests WHERE repository_id = ?)`,
		repositoryID, repositoryID, repositoryID).Scan(&result.ManifestCount, &result.TagCount, &lastPushed)
	result.LastPushedAtMillis = lastPushed.Int64
	return result, err
}

func (store *Store) RegistryManifests(ctx context.Context, repositoryID, afterDigest string, limit int) ([]RegistryManifest, bool, error) {
	if limit < 1 || limit > 1000 {
		return nil, false, errors.New("registry manifest list limit must be 1..1000")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT repository_id, digest, media_type, body, pushed_at
FROM registry_manifests
WHERE repository_id = ? AND digest > ?
ORDER BY digest LIMIT ?`, repositoryID, afterDigest, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]RegistryManifest, 0, limit)
	for rows.Next() {
		var manifest RegistryManifest
		if err := rows.Scan(&manifest.RepositoryID, &manifest.Digest, &manifest.MediaType, &manifest.Body, &manifest.PushedAtMillis); err != nil {
			return nil, false, err
		}
		result = append(result, manifest)
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

func (store *Store) RegistryTagsForManifest(ctx context.Context, repositoryID, digest string) ([]RegistryTag, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT name, manifest_digest, updated_at FROM registry_tags
WHERE repository_id = ? AND manifest_digest = ? ORDER BY name`, repositoryID, digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RegistryTag, 0)
	for rows.Next() {
		var tag RegistryTag
		if err := rows.Scan(&tag.Name, &tag.ManifestDigest, &tag.UpdatedAtMillis); err != nil {
			return nil, err
		}
		result = append(result, tag)
	}
	return result, rows.Err()
}

type RegistryAdminMutation struct {
	RepositoryID         string
	Reference            string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) DeleteRegistryTag(ctx context.Context, input RegistryAdminMutation) (string, error) {
	if err := validateRegistryAdminMutation(input); err != nil {
		return "", err
	}
	var digest string
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		if err := transaction.QueryRowContext(ctx, `
SELECT manifest_digest FROM registry_tags WHERE repository_id = ? AND name = ?`,
			input.RepositoryID, input.Reference).Scan(&digest); errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryManifestNotFound
		} else if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
DELETE FROM registry_tags WHERE repository_id = ? AND name = ?`, input.RepositoryID, input.Reference); err != nil {
			return err
		}
		return insertRegistryAudit(ctx, transaction, input, "registry.tag.delete", map[string]any{
			"tag": input.Reference, "manifestDigest": digest,
		})
	})
	return digest, err
}

func (store *Store) DeleteRegistryManifest(ctx context.Context, input RegistryAdminMutation) ([]string, error) {
	if err := validateRegistryAdminMutation(input); err != nil {
		return nil, err
	}
	var tags []string
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM registry_manifests WHERE repository_id = ? AND digest = ?)`,
			input.RepositoryID, input.Reference).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrRegistryManifestNotFound
		}
		rows, err := transaction.QueryContext(ctx, `
SELECT name FROM registry_tags WHERE repository_id = ? AND manifest_digest = ? ORDER BY name`,
			input.RepositoryID, input.Reference)
		if err != nil {
			return err
		}
		for rows.Next() {
			var tag string
			if err := rows.Scan(&tag); err != nil {
				_ = rows.Close()
				return err
			}
			tags = append(tags, tag)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
DELETE FROM registry_manifests WHERE repository_id = ? AND digest = ?`,
			input.RepositoryID, input.Reference); err != nil {
			return err
		}
		return insertRegistryAudit(ctx, transaction, input, "registry.manifest.delete", map[string]any{
			"manifestDigest": input.Reference, "tags": tags,
		})
	})
	return tags, err
}

func (store *Store) DeleteRegistryRepository(ctx context.Context, input RegistryAdminMutation) error {
	if err := validateRegistryAdminMutation(input); err != nil {
		return err
	}
	if input.Reference != "" {
		return errors.New("delete registry repository input is invalid")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var name string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM registry_repositories WHERE id = ?", input.RepositoryID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryRepositoryNotFound
		} else if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM registry_repositories WHERE id = ?", input.RepositoryID); err != nil {
			return err
		}
		return insertRegistryAudit(ctx, transaction, input, "registry.repository.delete", map[string]any{"name": name})
	})
}

func validateRegistryAdminMutation(input RegistryAdminMutation) error {
	if input.RepositoryID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("registry admin mutation input is incomplete")
	}
	return validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail)
}

func insertRegistryAudit(ctx context.Context, transaction *sql.Tx, input RegistryAdminMutation, action string, metadata map[string]any) error {
	metadata["actorEmail"] = input.ActorEmail
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'registry_repository', ?, ?, 'succeeded', ?, ?)`,
		input.AuditEventID, input.ActorKind, input.ActorID, action, input.RepositoryID,
		nullableString(input.RequestCorrelationID), string(encoded), input.CreatedAtMillis)
	if err != nil {
		return fmt.Errorf("record registry audit: %w", err)
	}
	return nil
}

type RegistryCleanupAudit struct {
	RepositoryID         string
	DeletedBlobCount     int
	DeletedBytes         int64
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) RecordRegistryCleanup(ctx context.Context, input RegistryCleanupAudit) error {
	if input.RepositoryID == "" || input.DeletedBlobCount < 0 || input.DeletedBytes < 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("registry cleanup audit input is invalid")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM registry_repositories WHERE id = ?)", input.RepositoryID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrRegistryRepositoryNotFound
		}
		return insertRegistryAudit(ctx, transaction, RegistryAdminMutation{
			RepositoryID: input.RepositoryID, AuditEventID: input.AuditEventID,
			ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			RequestCorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
		}, "registry.cleanup", map[string]any{
			"deletedBlobCount": input.DeletedBlobCount, "deletedBytes": input.DeletedBytes,
		})
	})
}
