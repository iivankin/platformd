package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrRecoveryNotActive = errors.New("installation is not in recovery mode")

type CompleteRecovery struct {
	InstallationID    string
	AuditEventID      string
	CompletedAtMillis int64
}

func (store *Store) CompleteRecovery(ctx context.Context, input CompleteRecovery) error {
	if input.InstallationID == "" || input.AuditEventID == "" || input.CompletedAtMillis <= 0 {
		return errors.New("complete recovery input is invalid")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE installation SET recovery_mode = 0, updated_at = ?
WHERE singleton = 1 AND id = ? AND recovery_mode = 1`,
			input.CompletedAtMillis, input.InstallationID,
		)
		if err != nil {
			return fmt.Errorf("leave recovery mode: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count recovery mode updates: %w", err)
		}
		if changed != 1 {
			return ErrRecoveryNotActive
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'system', 'disaster_restore', 'recovery.complete', 'installation', ?,
          'succeeded', '{}', ?)`,
			input.AuditEventID, input.InstallationID, input.CompletedAtMillis,
		); err != nil {
			return fmt.Errorf("audit recovery completion: %w", err)
		}
		return nil
	})
}
