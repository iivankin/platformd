package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	DefaultAuditPageSize = 50
	MaximumAuditPageSize = 200
	MaximumAuditCleanup  = 1000
)

var (
	ErrAuditPageInvalid   = errors.New("audit page is invalid")
	ErrAuditCursorInvalid = errors.New("audit cursor is invalid")
)

type AuditEvent struct {
	ID                   string
	ActorKind            string
	ActorID              string
	Action               string
	TargetKind           string
	TargetID             string
	RequestCorrelationID string
	Result               string
	Metadata             map[string]any
	CreatedAtMillis      int64
}

type AuditQuery struct {
	ActorKind  string
	Action     string
	Result     string
	TargetKind string
	TargetID   string
	Cursor     string
	Limit      int
}

type AuditPage struct {
	Events     []AuditEvent
	NextCursor string
}

func (store *Store) AuditEvents(ctx context.Context, query AuditQuery) (AuditPage, error) {
	if query.Limit == 0 {
		query.Limit = DefaultAuditPageSize
	}
	if query.Limit < 1 || query.Limit > MaximumAuditPageSize || !validAuditFilter("actor", query.ActorKind) ||
		!validAuditFilter("result", query.Result) || len(query.Action) > 128 || len(query.TargetKind) > 128 || len(query.TargetID) > 128 {
		return AuditPage{}, fmt.Errorf("%w: invalid filters or page size", ErrAuditPageInvalid)
	}
	var cursorCreated int64
	if query.Cursor != "" {
		err := store.database.QueryRowContext(ctx, "SELECT created_at FROM audit_events WHERE id = ?", query.Cursor).Scan(&cursorCreated)
		if errors.Is(err, sql.ErrNoRows) {
			return AuditPage{}, ErrAuditCursorInvalid
		}
		if err != nil {
			return AuditPage{}, fmt.Errorf("load audit cursor: %w", err)
		}
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, actor_kind, actor_id, action, target_kind, target_id,
       request_correlation_id, result, metadata_json, created_at
FROM audit_events
WHERE (? = '' OR actor_kind = ?)
  AND (? = '' OR action = ?)
  AND (? = '' OR result = ?)
  AND (? = '' OR target_kind = ?)
  AND (? = '' OR target_id = ?)
  AND (? = '' OR created_at < ? OR (created_at = ? AND id < ?))
ORDER BY created_at DESC, id DESC
LIMIT ?`,
		query.ActorKind, query.ActorKind, query.Action, query.Action, query.Result, query.Result,
		query.TargetKind, query.TargetKind, query.TargetID, query.TargetID,
		query.Cursor, cursorCreated, cursorCreated, query.Cursor, query.Limit+1,
	)
	if err != nil {
		return AuditPage{}, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]AuditEvent, 0, query.Limit+1)
	for rows.Next() {
		event, scanErr := scanAuditEvent(rows)
		if scanErr != nil {
			return AuditPage{}, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, fmt.Errorf("iterate audit events: %w", err)
	}
	page := AuditPage{Events: events}
	if len(events) > query.Limit {
		page.Events = events[:query.Limit]
		page.NextCursor = page.Events[len(page.Events)-1].ID
	}
	return page, nil
}

func (store *Store) CleanupAuditEvents(ctx context.Context, beforeMillis int64, limit int) (int64, error) {
	if beforeMillis <= 0 || limit < 1 || limit > MaximumAuditCleanup {
		return 0, errors.New("audit cleanup input is invalid")
	}
	var deleted int64
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM audit_events WHERE id IN (
  SELECT id FROM audit_events WHERE created_at < ? ORDER BY created_at, id LIMIT ?
)`, beforeMillis, limit)
		if err != nil {
			return fmt.Errorf("delete expired audit events: %w", err)
		}
		deleted, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted audit events: %w", err)
		}
		return nil
	})
	return deleted, err
}

func validAuditFilter(kind, value string) bool {
	if value == "" {
		return true
	}
	allowed := map[string]map[string]struct{}{
		"actor":  {"access": {}, "token": {}, "system": {}, "local_root": {}},
		"result": {"succeeded": {}, "failed": {}},
	}
	_, ok := allowed[kind][value]
	return ok
}

type auditScanner interface {
	Scan(...any) error
}

func scanAuditEvent(scanner auditScanner) (AuditEvent, error) {
	var event AuditEvent
	var correlationID sql.NullString
	var metadataJSON string
	if err := scanner.Scan(
		&event.ID, &event.ActorKind, &event.ActorID, &event.Action, &event.TargetKind, &event.TargetID,
		&correlationID, &event.Result, &metadataJSON, &event.CreatedAtMillis,
	); err != nil {
		return AuditEvent{}, fmt.Errorf("scan audit event: %w", err)
	}
	event.RequestCorrelationID = correlationID.String
	if err := json.Unmarshal([]byte(metadataJSON), &event.Metadata); err != nil {
		return AuditEvent{}, fmt.Errorf("decode audit metadata: %w", err)
	}
	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}
	return event, nil
}
