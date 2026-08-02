package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type RecordObjectStoreRestore struct {
	ObjectStoreID        string
	ObjectCount          int
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) RecordObjectStoreRestore(ctx context.Context, input RecordObjectStoreRestore) error {
	if input.ObjectStoreID == "" || input.ObjectCount < 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("object store restore audit input is incomplete")
	}
	if input.ActorKind == "system" {
		if input.ActorID == "" || input.ActorEmail != "" {
			return errors.New("system restore actor is invalid")
		}
	} else if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	metadataFields := map[string]any{
		"actorEmail":  input.ActorEmail,
		"objectCount": input.ObjectCount,
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var projectID, name string
		if err := transaction.QueryRowContext(ctx,
			"SELECT project_id, name FROM object_stores WHERE id = ?", input.ObjectStoreID,
		).Scan(&projectID, &name); errors.Is(err, sql.ErrNoRows) {
			return ErrObjectStoreNotFound
		} else if err != nil {
			return err
		}
		metadataFields["name"] = name
		metadata, err := json.Marshal(metadataFields)
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'object_store.restore', 'object_store', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, projectID, input.ActorKind, input.ActorID, input.ObjectStoreID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis,
		)
		return err
	})
}
