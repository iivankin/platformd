package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrProjectWebhookNotFound = errors.New("project webhook not found")

type ProjectWebhook struct {
	ID              string
	ProjectID       string
	URL             string
	EventTypes      []string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type ProjectWebhookMutation struct {
	Webhook              ProjectWebhook
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
}

type DeleteProjectWebhook struct {
	ID                   string
	ProjectID            string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) ProjectWebhooks(ctx context.Context, projectID string) ([]ProjectWebhook, error) {
	if _, err := store.Project(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, url, event_types_json, created_at, updated_at
FROM project_webhooks
WHERE project_id = ?
ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project webhooks: %w", err)
	}
	defer rows.Close()
	webhooks := make([]ProjectWebhook, 0)
	for rows.Next() {
		webhook, scanErr := scanProjectWebhook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		webhooks = append(webhooks, webhook)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project webhooks: %w", err)
	}
	return webhooks, nil
}

func (store *Store) CreateProjectWebhook(ctx context.Context, input ProjectWebhookMutation) (ProjectWebhook, error) {
	webhook := input.Webhook
	if webhook.ID == "" || webhook.ProjectID == "" || webhook.URL == "" || len(webhook.EventTypes) == 0 ||
		webhook.CreatedAtMillis <= 0 || webhook.UpdatedAtMillis != webhook.CreatedAtMillis ||
		input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" {
		return ProjectWebhook{}, errors.New("create project webhook input is incomplete")
	}
	eventTypesJSON, metadataJSON, err := projectWebhookMutationJSON(webhook, input.ActorEmail)
	if err != nil {
		return ProjectWebhook{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", webhook.ProjectID).Scan(&exists); err != nil {
			return fmt.Errorf("check project webhook project: %w", err)
		}
		if exists != 1 {
			return ErrProjectNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO project_webhooks(id, project_id, url, event_types_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, webhook.ID, webhook.ProjectID, webhook.URL, eventTypesJSON, webhook.CreatedAtMillis, webhook.UpdatedAtMillis); err != nil {
			return fmt.Errorf("create project webhook: %w", err)
		}
		return insertProjectWebhookAudit(ctx, transaction, webhook.ProjectID, input.AuditEventID, input.ActorID, "project_webhook.create", webhook.ID, input.RequestCorrelationID, metadataJSON, webhook.CreatedAtMillis)
	})
	return webhook, err
}

func (store *Store) UpdateProjectWebhook(ctx context.Context, input ProjectWebhookMutation) (ProjectWebhook, error) {
	webhook := input.Webhook
	if webhook.ID == "" || webhook.ProjectID == "" || webhook.URL == "" || len(webhook.EventTypes) == 0 ||
		webhook.UpdatedAtMillis <= 0 || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" {
		return ProjectWebhook{}, errors.New("update project webhook input is incomplete")
	}
	eventTypesJSON, metadataJSON, err := projectWebhookMutationJSON(webhook, input.ActorEmail)
	if err != nil {
		return ProjectWebhook{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := transaction.QueryRowContext(ctx, `
SELECT created_at FROM project_webhooks WHERE id = ? AND project_id = ?`, webhook.ID, webhook.ProjectID).Scan(&webhook.CreatedAtMillis); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectWebhookNotFound
		} else if err != nil {
			return fmt.Errorf("load project webhook before update: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE project_webhooks
SET url = ?, event_types_json = ?, updated_at = ?
WHERE id = ? AND project_id = ?`, webhook.URL, eventTypesJSON, webhook.UpdatedAtMillis, webhook.ID, webhook.ProjectID)
		if err != nil {
			return fmt.Errorf("update project webhook: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count updated project webhook: %w", err)
		}
		if changed != 1 {
			return ErrProjectWebhookNotFound
		}
		return insertProjectWebhookAudit(ctx, transaction, webhook.ProjectID, input.AuditEventID, input.ActorID, "project_webhook.update", webhook.ID, input.RequestCorrelationID, metadataJSON, webhook.UpdatedAtMillis)
	})
	return webhook, err
}

func (store *Store) DeleteProjectWebhook(ctx context.Context, input DeleteProjectWebhook) error {
	if input.ID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete project webhook input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var url string
		if err := transaction.QueryRowContext(ctx, `
SELECT url FROM project_webhooks WHERE id = ? AND project_id = ?`, input.ID, input.ProjectID).Scan(&url); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectWebhookNotFound
		} else if err != nil {
			return fmt.Errorf("load project webhook before delete: %w", err)
		}
		metadataJSON, err := json.Marshal(map[string]string{
			"actorEmail": input.ActorEmail, "projectId": input.ProjectID, "url": url,
		})
		if err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM project_webhooks WHERE id = ? AND project_id = ?", input.ID, input.ProjectID)
		if err != nil {
			return fmt.Errorf("delete project webhook: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted project webhook: %w", err)
		}
		if changed != 1 {
			return ErrProjectWebhookNotFound
		}
		return insertProjectWebhookAudit(ctx, transaction, input.ProjectID, input.AuditEventID, input.ActorID, "project_webhook.delete", input.ID, input.RequestCorrelationID, metadataJSON, input.DeletedAtMillis)
	})
}

func scanProjectWebhook(scanner interface{ Scan(...any) error }) (ProjectWebhook, error) {
	var webhook ProjectWebhook
	var eventTypesJSON string
	if err := scanner.Scan(&webhook.ID, &webhook.ProjectID, &webhook.URL, &eventTypesJSON, &webhook.CreatedAtMillis, &webhook.UpdatedAtMillis); err != nil {
		return ProjectWebhook{}, fmt.Errorf("scan project webhook: %w", err)
	}
	if err := json.Unmarshal([]byte(eventTypesJSON), &webhook.EventTypes); err != nil {
		return ProjectWebhook{}, fmt.Errorf("decode project webhook event types: %w", err)
	}
	return webhook, nil
}

func projectWebhookMutationJSON(webhook ProjectWebhook, actorEmail string) (string, []byte, error) {
	eventTypesJSON, err := json.Marshal(webhook.EventTypes)
	if err != nil {
		return "", nil, fmt.Errorf("encode project webhook event types: %w", err)
	}
	metadataJSON, err := json.Marshal(map[string]any{
		"actorEmail": actorEmail,
		"eventTypes": webhook.EventTypes,
		"projectId":  webhook.ProjectID,
		"url":        webhook.URL,
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode project webhook audit metadata: %w", err)
	}
	return string(eventTypesJSON), metadataJSON, nil
}

func insertProjectWebhookAudit(ctx context.Context, transaction *sql.Tx, projectID, id, actorID, action, targetID, correlationID string, metadata []byte, timestamp int64) error {
	var requestID any
	if correlationID != "" {
		requestID = correlationID
	}
	_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, 'access', ?, ?, 'project_webhook', ?, ?, 'succeeded', ?, ?)`,
		id, projectID, actorID, action, targetID, requestID, string(metadata), timestamp,
	)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
