package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type RecordPortForwardTicket struct {
	AuditEventID    string
	ActorTokenID    string
	TicketID        string
	ProjectID       string
	ResourceKind    string
	ResourceID      string
	Port            int
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

func (store *Store) RecordPortForwardTicket(ctx context.Context, input RecordPortForwardTicket) error {
	if input.AuditEventID == "" || input.ActorTokenID == "" || input.TicketID == "" ||
		input.ProjectID == "" || input.ResourceID == "" || input.Port < 1 || input.Port > 65535 ||
		input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis {
		return errors.New("port forward ticket audit input is incomplete")
	}
	switch input.ResourceKind {
	case "service", "postgres", "redis":
	default:
		return errors.New("port forward ticket audit resource kind is invalid")
	}
	metadata, err := json.Marshal(map[string]any{
		"ticketId": input.TicketID, "projectId": input.ProjectID,
		"resourceKind": input.ResourceKind, "resourceId": input.ResourceID,
		"port": input.Port, "expiresAt": input.ExpiresAtMillis,
	})
	if err != nil {
		return fmt.Errorf("encode port forward ticket audit: %w", err)
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'token', ?, 'port_forward.ticket.create', 'port_forward_ticket', ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorTokenID, input.TicketID, string(metadata), input.CreatedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("audit port forward ticket: %w", err)
		}
		return nil
	})
}
