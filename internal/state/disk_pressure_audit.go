package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/diskpressure"
)

type DiskPressureAuditInput struct {
	ID              string
	InstallationID  string
	From            diskpressure.Level
	To              diskpressure.Level
	Usage           diskpressure.Usage
	CreatedAtMillis int64
}

func (store *Store) AppendDiskPressureAudit(ctx context.Context, input DiskPressureAuditInput) error {
	if input.ID == "" || input.InstallationID == "" || input.CreatedAtMillis <= 0 || input.From == input.To || !validPressureLevel(input.From) || !validPressureLevel(input.To) {
		return errors.New("disk pressure audit input is invalid")
	}
	metadata, err := json.Marshal(map[string]any{
		"from": input.From, "to": input.To,
		"byteBasisPoints": input.Usage.ByteBasisPoints, "inodeBasisPoints": input.Usage.InodeBasisPoints,
		"totalBytes": input.Usage.TotalBytes, "availableBytes": input.Usage.AvailableBytes,
		"totalInodes": input.Usage.TotalInodes, "availableInodes": input.Usage.AvailableInodes,
	})
	if err != nil {
		return fmt.Errorf("encode disk pressure audit: %w", err)
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'system', 'platformd', 'disk_pressure.transition', 'installation', ?, 'succeeded', ?, ?)`,
			input.ID, input.InstallationID, string(metadata), input.CreatedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("append disk pressure audit: %w", err)
		}
		return nil
	})
}

func validPressureLevel(level diskpressure.Level) bool {
	return level == diskpressure.Normal || level == diskpressure.Low || level == diskpressure.Critical || level == diskpressure.Emergency
}
