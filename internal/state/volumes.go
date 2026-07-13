package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

const maximumVolumeOwnerID = int64(1<<32 - 2)

var (
	ErrVolumeNotFound     = errors.New("volume not found")
	ErrVolumeNameConflict = errors.New("volume name already exists for this service")
	ErrVolumeInUse        = errors.New("volume is referenced by desired or active service configuration")
)

type Volume struct {
	ID              string
	ProjectID       string
	ServiceID       string
	Name            string
	OwnerUID        int
	OwnerGID        int
	CreatedAtMillis int64
}

type CreateVolume struct {
	Volume
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
}

type DeleteVolume struct {
	ProjectID            string
	ServiceID            string
	VolumeID             string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) CreateVolume(ctx context.Context, input CreateVolume) (Volume, error) {
	volume := input.Volume
	if volume.ID == "" || volume.ProjectID == "" || volume.ServiceID == "" ||
		input.AuditEventID == "" || volume.CreatedAtMillis <= 0 ||
		validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return Volume{}, errors.New("create volume input is incomplete")
	}
	if err := resourcename.Validate(volume.Name); err != nil {
		return Volume{}, err
	}
	if !validVolumeOwner(volume.OwnerUID) || !validVolumeOwner(volume.OwnerGID) {
		return Volume{}, fmt.Errorf("volume owner IDs must be between 0 and %d", maximumVolumeOwnerID)
	}
	metadata, err := volumeAuditMetadata(input.ActorEmail, volume.ServiceID, volume.OwnerUID, volume.OwnerGID)
	if err != nil {
		return Volume{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireVolumeService(ctx, transaction, volume.ProjectID, volume.ServiceID); err != nil {
			return err
		}
		var existing string
		err := transaction.QueryRowContext(ctx, `
SELECT id FROM volumes WHERE service_id = ? AND name = ?`, volume.ServiceID, volume.Name).Scan(&existing)
		if err == nil {
			return ErrVolumeNameConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check volume name: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO volumes(id, project_id, service_id, name, owner_uid, owner_gid, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, volume.ID, volume.ProjectID, volume.ServiceID,
			volume.Name, volume.OwnerUID, volume.OwnerGID, volume.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create volume: %w", err)
		}
		return insertVolumeAudit(ctx, transaction, volume.ID, input.AuditEventID,
			input.ActorKind, input.ActorID, input.RequestCorrelationID,
			"volume.create", metadata, volume.CreatedAtMillis,
		)
	})
	if err != nil {
		return Volume{}, err
	}
	return volume, nil
}

func (store *Store) VolumesByService(ctx context.Context, projectID, serviceID string) ([]Volume, error) {
	if err := store.requireService(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, service_id, name, owner_uid, owner_gid, created_at
FROM volumes WHERE project_id = ? AND service_id = ? ORDER BY name, id`, projectID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service volumes: %w", err)
	}
	defer rows.Close()
	volumes := make([]Volume, 0)
	for rows.Next() {
		volume, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, volume)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service volumes: %w", err)
	}
	return volumes, nil
}

func (store *Store) DeleteVolume(ctx context.Context, input DeleteVolume) (Volume, error) {
	if input.ProjectID == "" || input.ServiceID == "" || input.VolumeID == "" ||
		input.AuditEventID == "" || input.DeletedAtMillis <= 0 ||
		validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return Volume{}, errors.New("delete volume input is incomplete")
	}
	var deleted Volume
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var err error
		deleted, err = volumeInTransaction(ctx, transaction, input.ProjectID, input.ServiceID, input.VolumeID)
		if err != nil {
			return err
		}
		inUse, err := volumeInUse(ctx, transaction, deleted)
		if err != nil {
			return err
		}
		if inUse {
			return ErrVolumeInUse
		}
		result, err := transaction.ExecContext(ctx, `
DELETE FROM volumes WHERE id = ? AND project_id = ? AND service_id = ?`,
			deleted.ID, deleted.ProjectID, deleted.ServiceID,
		)
		if err != nil {
			return fmt.Errorf("delete volume: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted volumes: %w", err)
		}
		if changed != 1 {
			return ErrVolumeNotFound
		}
		metadata, err := volumeAuditMetadata(input.ActorEmail, deleted.ServiceID, deleted.OwnerUID, deleted.OwnerGID)
		if err != nil {
			return err
		}
		return insertVolumeAudit(ctx, transaction, deleted.ID, input.AuditEventID,
			input.ActorKind, input.ActorID, input.RequestCorrelationID,
			"volume.delete", metadata, input.DeletedAtMillis,
		)
	})
	return deleted, err
}

func (store *Store) requireService(ctx context.Context, projectID, serviceID string) error {
	var exists int
	if err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM services WHERE id = ? AND project_id = ?)`, serviceID, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("check volume service: %w", err)
	}
	if exists != 1 {
		return ErrServiceNotFound
	}
	return nil
}

func requireVolumeService(ctx context.Context, transaction *sql.Tx, projectID, serviceID string) error {
	var exists int
	if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM services WHERE id = ? AND project_id = ?)`, serviceID, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("check volume service: %w", err)
	}
	if exists != 1 {
		return ErrServiceNotFound
	}
	return nil
}

type volumeScanner interface {
	Scan(...any) error
}

func scanVolume(scanner volumeScanner) (Volume, error) {
	var volume Volume
	if err := scanner.Scan(&volume.ID, &volume.ProjectID, &volume.ServiceID, &volume.Name,
		&volume.OwnerUID, &volume.OwnerGID, &volume.CreatedAtMillis); err != nil {
		return Volume{}, fmt.Errorf("scan volume: %w", err)
	}
	return volume, nil
}

func volumeInTransaction(ctx context.Context, transaction *sql.Tx, projectID, serviceID, volumeID string) (Volume, error) {
	volume, err := scanVolume(transaction.QueryRowContext(ctx, `
SELECT id, project_id, service_id, name, owner_uid, owner_gid, created_at
FROM volumes WHERE id = ? AND project_id = ? AND service_id = ?`, volumeID, projectID, serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Volume{}, ErrVolumeNotFound
	}
	return volume, err
}

func volumeInUse(ctx context.Context, transaction *sql.Tx, volume Volume) (bool, error) {
	var desired int
	if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM service_volume_mounts WHERE service_id = ? AND volume_id = ?)`,
		volume.ServiceID, volume.ID,
	).Scan(&desired); err != nil {
		return false, fmt.Errorf("check desired volume reference: %w", err)
	}
	if desired == 1 {
		return true, nil
	}
	var snapshotJSON sql.NullString
	if err := transaction.QueryRowContext(ctx, `
SELECT d.snapshot_json
FROM services s LEFT JOIN deployments d ON d.id = s.active_deployment_id
WHERE s.id = ? AND s.project_id = ?`, volume.ServiceID, volume.ProjectID).Scan(&snapshotJSON); err != nil {
		return false, fmt.Errorf("load active deployment volume references: %w", err)
	}
	if !snapshotJSON.Valid {
		return false, nil
	}
	var snapshot serviceconfig.Snapshot
	if err := json.Unmarshal([]byte(snapshotJSON.String), &snapshot); err != nil {
		return false, fmt.Errorf("decode active deployment volume references: %w", err)
	}
	for _, mount := range snapshot.VolumeMounts {
		if mount.VolumeID == volume.ID {
			return true, nil
		}
	}
	return false, nil
}

func insertVolumeAudit(
	ctx context.Context,
	transaction *sql.Tx,
	volumeID, auditID, actorKind, actorID, correlationID, action string,
	metadata []byte,
	timestampMillis int64,
) error {
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'volume', ?, ?, 'succeeded', ?, ?)`,
		auditID, actorKind, actorID, action, volumeID, nullableString(correlationID),
		string(metadata), timestampMillis,
	); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func volumeAuditMetadata(actorEmail, serviceID string, ownerUID, ownerGID int) ([]byte, error) {
	metadata := map[string]string{
		"ownerGid": strconv.Itoa(ownerGID), "ownerUid": strconv.Itoa(ownerUID),
		"serviceId": serviceID,
	}
	if actorEmail != "" {
		metadata["actorEmail"] = actorEmail
	}
	return json.Marshal(metadata)
}

func validVolumeOwner(value int) bool {
	return value >= 0 && int64(value) <= maximumVolumeOwnerID
}
