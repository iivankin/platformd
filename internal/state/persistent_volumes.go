package state

import (
	"context"
	"errors"
	"fmt"
)

type PersistentVolumeKind string

const (
	PersistentVolumeOrdinary PersistentVolumeKind = "ordinary"
	PersistentVolumePostgres PersistentVolumeKind = "postgres"
	PersistentVolumeRedis    PersistentVolumeKind = "redis"
)

type PersistentVolumeReference struct {
	ProjectID string
	VolumeID  string
	Kind      PersistentVolumeKind
	OwnerUID  int
	OwnerGID  int
}

// PersistentVolumeReferences returns every authoritative filesystem volume
// pointer. Restore candidates and superseded database volumes intentionally do
// not have rows and are therefore eligible for startup cleanup.
func (store *Store) PersistentVolumeReferences(ctx context.Context) ([]PersistentVolumeReference, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT project_id, id, 'ordinary', owner_uid, owner_gid FROM volumes
UNION ALL
SELECT project_id, volume_id, 'postgres', 0, 0 FROM managed_postgres
UNION ALL
SELECT project_id, volume_id, 'redis', 0, 0 FROM managed_redis
ORDER BY 1, 2, 3`)
	if err != nil {
		return nil, fmt.Errorf("list persistent volume references: %w", err)
	}
	defer rows.Close()

	var result []PersistentVolumeReference
	seen := make(map[string]PersistentVolumeKind)
	for rows.Next() {
		var reference PersistentVolumeReference
		if err := rows.Scan(
			&reference.ProjectID, &reference.VolumeID, &reference.Kind,
			&reference.OwnerUID, &reference.OwnerGID,
		); err != nil {
			return nil, fmt.Errorf("scan persistent volume reference: %w", err)
		}
		key := reference.ProjectID + "\x00" + reference.VolumeID
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf(
				"persistent volume path is shared by %s and %s: %s/%s",
				previous, reference.Kind, reference.ProjectID, reference.VolumeID,
			)
		}
		if reference.ProjectID == "" || reference.VolumeID == "" ||
			!validVolumeOwner(reference.OwnerUID) || !validVolumeOwner(reference.OwnerGID) {
			return nil, errors.New("persistent volume reference is invalid")
		}
		seen[key] = reference.Kind
		result = append(result, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate persistent volume references: %w", err)
	}
	return result, nil
}
