package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

const MaximumProjectIconBytes = 128 << 10

var (
	ErrProjectIconNotFound = errors.New("project icon not found")
	ErrInvalidProjectIcon  = errors.New("project icon must be a PNG, JPEG, or WebP image up to 128 KiB")
)

type ProjectIcon struct {
	Bytes       []byte
	ContentType string
}

type SetProjectIconInput struct {
	ProjectID            string
	Bytes                []byte
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

type ClearProjectIconInput struct {
	ProjectID            string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func DetectProjectIconContentType(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaximumProjectIconBytes {
		return "", ErrInvalidProjectIcon
	}
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return "", ErrInvalidProjectIcon
		}
		return "image/png", nil
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return "", ErrInvalidProjectIcon
		}
		return "image/jpeg", nil
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", nil
	default:
		return "", ErrInvalidProjectIcon
	}
}

func (store *Store) ProjectIcon(ctx context.Context, projectID string) (ProjectIcon, error) {
	if projectID == "" {
		return ProjectIcon{}, errors.New("project ID is empty")
	}
	var icon ProjectIcon
	var contentType sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT icon_bytes, icon_content_type FROM projects WHERE id = ?`, projectID).Scan(&icon.Bytes, &contentType)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectIcon{}, ErrProjectNotFound
	}
	if err != nil {
		return ProjectIcon{}, fmt.Errorf("load project icon: %w", err)
	}
	if len(icon.Bytes) == 0 || !contentType.Valid || contentType.String == "" {
		return ProjectIcon{}, ErrProjectIconNotFound
	}
	icon.ContentType = contentType.String
	return icon, nil
}

func (store *Store) SetProjectIcon(ctx context.Context, input SetProjectIconInput) (ProjectSummary, error) {
	if input.ProjectID == "" || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 ||
		validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ProjectSummary{}, errors.New("set project icon input is incomplete")
	}
	contentType, err := DetectProjectIconContentType(input.Bytes)
	if err != nil {
		return ProjectSummary{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE projects SET icon_bytes = ?, icon_content_type = ?, updated_at = ?
WHERE id = ?`, input.Bytes, contentType, input.UpdatedAtMillis, input.ProjectID)
		if err != nil {
			return fmt.Errorf("set project icon: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count project icon update: %w", err)
		}
		if changed != 1 {
			return ErrProjectNotFound
		}
		return insertProjectIconAudit(ctx, transaction, projectIconAudit{
			ID: input.AuditEventID, ProjectID: input.ProjectID, ActorKind: input.ActorKind,
			ActorID: input.ActorID, ActorEmail: input.ActorEmail, Action: "project.icon.set",
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.UpdatedAtMillis,
			Metadata: map[string]string{
				"contentType": contentType,
				"bytes":       fmt.Sprintf("%d", len(input.Bytes)),
			},
		})
	})
	if err != nil {
		return ProjectSummary{}, err
	}
	return store.Project(ctx, input.ProjectID)
}

func (store *Store) ClearProjectIcon(ctx context.Context, input ClearProjectIconInput) (ProjectSummary, error) {
	if input.ProjectID == "" || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 ||
		validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ProjectSummary{}, errors.New("clear project icon input is incomplete")
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var hasIcon int
		err := transaction.QueryRowContext(ctx, `
SELECT CASE WHEN icon_bytes IS NULL THEN 0 ELSE 1 END FROM projects WHERE id = ?`, input.ProjectID).Scan(&hasIcon)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		}
		if err != nil {
			return fmt.Errorf("load project icon presence: %w", err)
		}
		if hasIcon != 1 {
			return ErrProjectIconNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE projects SET icon_bytes = NULL, icon_content_type = NULL, updated_at = ?
WHERE id = ?`, input.UpdatedAtMillis, input.ProjectID); err != nil {
			return fmt.Errorf("clear project icon: %w", err)
		}
		return insertProjectIconAudit(ctx, transaction, projectIconAudit{
			ID: input.AuditEventID, ProjectID: input.ProjectID, ActorKind: input.ActorKind,
			ActorID: input.ActorID, ActorEmail: input.ActorEmail, Action: "project.icon.clear",
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.UpdatedAtMillis,
		})
	})
	if err != nil {
		return ProjectSummary{}, err
	}
	return store.Project(ctx, input.ProjectID)
}

type projectIconAudit struct {
	ID              string
	ProjectID       string
	ActorKind       string
	ActorID         string
	ActorEmail      string
	Action          string
	CorrelationID   string
	CreatedAtMillis int64
	Metadata        map[string]string
}

func insertProjectIconAudit(ctx context.Context, transaction *sql.Tx, audit projectIconAudit) error {
	if err := validateMutationActor(audit.ActorKind, audit.ActorID, audit.ActorEmail); err != nil {
		return err
	}
	metadata := make(map[string]string)
	if audit.ActorEmail != "" {
		metadata["actorEmail"] = audit.ActorEmail
	}
	for key, value := range audit.Metadata {
		metadata[key] = value
	}
	var name string
	if err := transaction.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, audit.ProjectID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return ErrProjectNotFound
	} else if err != nil {
		return fmt.Errorf("load project icon audit target: %w", err)
	}
	metadata["name"] = name
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var correlationID any
	if audit.CorrelationID != "" {
		correlationID = audit.CorrelationID
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, 'project', ?, ?, 'succeeded', ?, ?)`,
		audit.ID, audit.ProjectID, audit.ActorKind, audit.ActorID, audit.Action, audit.ProjectID,
		correlationID, string(encoded), audit.CreatedAtMillis,
	); err != nil {
		return fmt.Errorf("audit %s: %w", audit.Action, err)
	}
	return nil
}
