package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/backupcron"
	"github.com/iivankin/platformd/internal/registryname"
)

type RestoreRegistryRepository struct {
	RepositoryID         string
	Manifests            []RegistryManifest
	Tags                 []RegistryTag
	ApplySnapshotPolicy  bool
	PublicPull           bool
	BackupEnabled        bool
	BackupCron           string
	BackupRetentionCount int
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

// RestoreRegistryRepository replaces only repository content and, when
// explicitly requested, its mutable policy. Repository identity and robot
// credentials remain authoritative in the current control database.
func (store *Store) RestoreRegistryRepository(ctx context.Context, input RestoreRegistryRepository) error {
	if err := validateRestoreRegistryRepository(input); err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail":            input.ActorEmail,
		"manifestCount":         len(input.Manifests),
		"tagCount":              len(input.Tags),
		"appliedSnapshotPolicy": input.ApplySnapshotPolicy,
	})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM registry_repositories WHERE id = ?)", input.RepositoryID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrRegistryRepositoryNotFound
		}
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM registry_uploads WHERE repository_id = ?", input.RepositoryID,
		); err != nil {
			return fmt.Errorf("remove registry uploads before restore: %w", err)
		}
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM registry_tags WHERE repository_id = ?", input.RepositoryID,
		); err != nil {
			return fmt.Errorf("remove registry tags before restore: %w", err)
		}
		if _, err := transaction.ExecContext(ctx,
			"DELETE FROM registry_manifests WHERE repository_id = ?", input.RepositoryID,
		); err != nil {
			return fmt.Errorf("remove registry manifests before restore: %w", err)
		}
		for _, manifest := range input.Manifests {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_manifests(repository_id, digest, media_type, body, pushed_at)
VALUES (?, ?, ?, ?, ?)`, input.RepositoryID, manifest.Digest, manifest.MediaType,
				manifest.Body, manifest.PushedAtMillis); err != nil {
				return fmt.Errorf("restore registry manifest: %w", err)
			}
		}
		for _, tag := range input.Tags {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_tags(repository_id, name, manifest_digest, updated_at)
VALUES (?, ?, ?, ?)`, input.RepositoryID, tag.Name, tag.ManifestDigest,
				tag.UpdatedAtMillis); err != nil {
				return fmt.Errorf("restore registry tag: %w", err)
			}
		}
		if input.ApplySnapshotPolicy {
			enabled := 0
			if input.BackupEnabled {
				enabled = 1
			}
			publicPull := 0
			if input.PublicPull {
				publicPull = 1
			}
			if _, err := transaction.ExecContext(ctx, `
UPDATE registry_repositories
SET public_pull = ?, backup_enabled = ?, backup_cron = ?,
    backup_retention_count = ?, updated_at = ?
WHERE id = ?`, publicPull, enabled, nullableString(input.BackupCron),
				input.BackupRetentionCount, input.CreatedAtMillis, input.RepositoryID); err != nil {
				return fmt.Errorf("restore registry repository policy: %w", err)
			}
		} else if _, err := transaction.ExecContext(ctx,
			"UPDATE registry_repositories SET updated_at = ? WHERE id = ?",
			input.CreatedAtMillis, input.RepositoryID,
		); err != nil {
			return fmt.Errorf("mark registry repository restored: %w", err)
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'registry.restore', 'registry_repository', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.RepositoryID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis)
		return err
	})
}

func validateRestoreRegistryRepository(input RestoreRegistryRepository) error {
	if input.RepositoryID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 ||
		input.BackupRetentionCount < 1 || input.BackupRetentionCount > 100 {
		return errors.New("restore registry repository input is incomplete")
	}
	if input.ActorKind == "system" {
		if input.ActorID == "" || input.ActorEmail != "" {
			return errors.New("system registry restore actor is invalid")
		}
	} else if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	cron := ""
	if input.BackupCron != "" {
		var err error
		cron, err = backupcron.Canonical(input.BackupCron)
		if err != nil || cron != input.BackupCron {
			return errors.New("registry restore backup cron is invalid or non-canonical")
		}
	}
	if input.BackupEnabled && cron == "" {
		return errors.New("enabled restored registry backup policy requires cron")
	}
	manifests := make(map[string]struct{}, len(input.Manifests))
	for _, manifest := range input.Manifests {
		if manifest.RepositoryID != input.RepositoryID || registryname.ValidateDigest(manifest.Digest) != nil ||
			manifest.MediaType == "" || len(manifest.Body) == 0 || manifest.PushedAtMillis <= 0 {
			return errors.New("restored registry manifest metadata is invalid")
		}
		if _, duplicate := manifests[manifest.Digest]; duplicate {
			return errors.New("restored registry manifests contain duplicate digests")
		}
		manifests[manifest.Digest] = struct{}{}
	}
	tags := make(map[string]struct{}, len(input.Tags))
	for _, tag := range input.Tags {
		if registryname.ValidateTag(tag.Name) != nil || registryname.ValidateDigest(tag.ManifestDigest) != nil ||
			tag.UpdatedAtMillis <= 0 {
			return errors.New("restored registry tag metadata is invalid")
		}
		if _, exists := manifests[tag.ManifestDigest]; !exists {
			return errors.New("restored registry tag references a missing manifest")
		}
		if _, duplicate := tags[tag.Name]; duplicate {
			return errors.New("restored registry tags contain duplicate names")
		}
		tags[tag.Name] = struct{}{}
	}
	return nil
}
