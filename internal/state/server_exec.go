package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type RecordServerExec struct {
	AuditEventID         string
	ActorTokenID         string
	RequestCorrelationID string
	Succeeded            bool
	StartedAtMillis      int64
	FinishedAtMillis     int64
	DurationMillis       int64
	ExitCode             int
	TimedOut             bool
	Cancelled            bool
	StdoutTruncated      bool
	StderrTruncated      bool
	ExecutionError       bool
}

func (store *Store) RecordServerExec(ctx context.Context, input RecordServerExec) error {
	if input.AuditEventID == "" || input.ActorTokenID == "" || input.StartedAtMillis <= 0 ||
		input.FinishedAtMillis < input.StartedAtMillis || input.DurationMillis < 0 {
		return errors.New("server exec audit input is incomplete")
	}
	metadata, err := json.Marshal(map[string]any{
		"startedAt": input.StartedAtMillis, "finishedAt": input.FinishedAtMillis,
		"durationMillis": input.DurationMillis, "exitCode": input.ExitCode,
		"timedOut": input.TimedOut, "cancelled": input.Cancelled,
		"stdoutTruncated": input.StdoutTruncated, "stderrTruncated": input.StderrTruncated,
		"executionError": input.ExecutionError,
	})
	if err != nil {
		return fmt.Errorf("encode server exec audit metadata: %w", err)
	}
	result := "failed"
	if input.Succeeded {
		result = "succeeded"
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var correlationID any
		if input.RequestCorrelationID != "" {
			correlationID = input.RequestCorrelationID
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'token', ?, 'server.exec', 'server', 'host', ?, ?, ?, ?)`,
			input.AuditEventID, input.ActorTokenID, correlationID, result, string(metadata), input.FinishedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("audit server exec: %w", err)
		}
		return nil
	})
}
