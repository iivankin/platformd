package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// VolumeInitialized reports whether the volume has completed its first
// container mount. The marker outlives disposable libpod state so an empty
// restored volume is never populated or chowned a second time.
func (store *Store) VolumeInitialized(
	ctx context.Context,
	projectID string,
	serviceID string,
	volumeID string,
) (bool, error) {
	if projectID == "" || serviceID == "" || volumeID == "" {
		return false, ErrVolumeNotFound
	}
	var volumeIDResult string
	var initializedAt sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT v.id, i.initialized_at
FROM volumes v
LEFT JOIN volume_initializations i ON i.volume_id = v.id
WHERE v.id = ? AND v.project_id = ? AND v.service_id = ?`,
		volumeID, projectID, serviceID,
	).Scan(&volumeIDResult, &initializedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrVolumeNotFound
	}
	if err != nil {
		return false, fmt.Errorf("inspect volume initialization: %w", err)
	}
	return initializedAt.Valid, nil
}

func (store *Store) RecordVolumeInitialization(
	ctx context.Context,
	projectID string,
	serviceID string,
	volumeID string,
	initializedAtMillis int64,
) error {
	if projectID == "" || serviceID == "" || volumeID == "" || initializedAtMillis <= 0 {
		return errors.New("volume initialization input is incomplete")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		if _, err := volumeInTransaction(ctx, transaction, projectID, serviceID, volumeID); err != nil {
			return err
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO volume_initializations(volume_id, initialized_at)
VALUES (?, ?)
ON CONFLICT(volume_id) DO NOTHING`, volumeID, initializedAtMillis)
		if err != nil {
			return fmt.Errorf("record volume initialization: %w", err)
		}
		return nil
	})
}
