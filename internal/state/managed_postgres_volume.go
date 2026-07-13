package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/managedimages"
)

type SwitchManagedPostgresVolume struct {
	ResourceID           string
	ExpectedVolumeID     string
	VolumeID             string
	ExpectedImageTag     string
	ExpectedImageDigest  string
	ImageTag             string
	ImageDigest          string
	Action               string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SwitchManagedPostgresVolume(ctx context.Context, input SwitchManagedPostgresVolume) error {
	if input.ResourceID == "" || input.ExpectedVolumeID == "" || input.VolumeID == "" ||
		input.ExpectedVolumeID == input.VolumeID || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 ||
		(input.Action != "postgres.restore" && input.Action != "postgres.version_change") {
		return errors.New("switch managed PostgreSQL volume input is invalid")
	}
	versionChange := input.Action == "postgres.version_change"
	imageSwitch := managedDatabaseImageSwitch{
		ExpectedTag: input.ExpectedImageTag, ExpectedDigest: input.ExpectedImageDigest,
		Tag: input.ImageTag, Digest: input.ImageDigest,
	}
	if versionChange {
		if err := validateManagedDatabaseImageSwitch(managedimages.PostgreSQL, imageSwitch); err != nil {
			return err
		}
	} else if imageSwitch != (managedDatabaseImageSwitch{}) {
		return errors.New("managed PostgreSQL restore cannot change the image")
	}
	if err := validateManagedVolumeSwitchActor(input.Action, input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	metadataFields := map[string]any{
		"previousVolumeId": input.ExpectedVolumeID, "volumeId": input.VolumeID,
	}
	if versionChange {
		metadataFields["previousImageTag"] = input.ExpectedImageTag
		metadataFields["previousImageDigest"] = input.ExpectedImageDigest
		metadataFields["imageTag"] = input.ImageTag
		metadataFields["imageDigest"] = input.ImageDigest
	}
	if input.ActorEmail != "" {
		metadataFields["actorEmail"] = input.ActorEmail
	}
	metadata, err := json.Marshal(metadataFields)
	if err != nil {
		return err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var result sql.Result
		if versionChange {
			result, err = transaction.ExecContext(ctx, `
UPDATE managed_postgres
SET volume_id = ?, image_tag = ?, image_digest = ?, updated_at = ?
WHERE id = ? AND volume_id = ? AND image_tag = ? AND image_digest = ?`,
				input.VolumeID, input.ImageTag, input.ImageDigest, input.UpdatedAtMillis,
				input.ResourceID, input.ExpectedVolumeID, input.ExpectedImageTag, input.ExpectedImageDigest,
			)
		} else {
			result, err = transaction.ExecContext(ctx, `
UPDATE managed_postgres SET volume_id = ?, updated_at = ?
WHERE id = ? AND volume_id = ?`, input.VolumeID, input.UpdatedAtMillis,
				input.ResourceID, input.ExpectedVolumeID,
			)
		}
		if err != nil {
			return fmt.Errorf("switch managed PostgreSQL active pointer: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			var exists int
			if err := transaction.QueryRowContext(ctx,
				"SELECT EXISTS(SELECT 1 FROM managed_postgres WHERE id = ?)", input.ResourceID,
			).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return ErrManagedPostgresNotFound
			}
			return errors.New("managed PostgreSQL active pointer changed concurrently")
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'postgres', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.Action, input.ResourceID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	return err
}

func validateManagedVolumeSwitchActor(action, kind, actorID, email string) error {
	if kind == "system" {
		if (action != "postgres.restore" && action != "redis.restore") || actorID == "" || email != "" {
			return errors.New("system managed database restore actor is invalid")
		}
		return nil
	}
	return validateMutationActor(kind, actorID, email)
}
