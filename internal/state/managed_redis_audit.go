package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type RecordManagedRedisDataMutation struct {
	ResourceID           string
	ProjectID            string
	Operation            string
	Result               string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) RecordManagedRedisDataMutation(ctx context.Context, input RecordManagedRedisDataMutation) error {
	if input.ResourceID == "" || input.ProjectID == "" || input.Operation == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("managed Redis data audit input is incomplete")
	}
	if input.Result != "succeeded" && input.Result != "failed" {
		return errors.New("managed Redis data audit result is invalid")
	}
	if err := validateMutationActor("access", input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]string{
		"actorEmail": input.ActorEmail, "operation": input.Operation,
	})
	if err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM managed_redis WHERE id = ? AND project_id = ?)`, input.ResourceID, input.ProjectID).Scan(&exists); err != nil {
			return fmt.Errorf("check managed Redis audit target: %w", err)
		}
		if exists == 0 {
			return ErrManagedRedisNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'redis.data.mutate', 'redis', ?, ?, ?, ?, ?)`,
			input.AuditEventID, input.ActorID, input.ResourceID,
			nullableString(input.RequestCorrelationID), input.Result, string(metadata), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit managed Redis data mutation: %w", err)
		}
		return nil
	})
}
