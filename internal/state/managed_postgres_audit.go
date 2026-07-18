package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type RecordManagedPostgresQuery struct {
	ResourceID           string
	ProjectID            string
	Result               string
	RowCount             int
	DurationMillis       int64
	ErrorClass           string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type RecordManagedPostgresExtension struct {
	ResourceID           string
	ProjectID            string
	ExtensionName        string
	Install              bool
	Result               string
	DurationMillis       int64
	ErrorClass           string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) RecordManagedPostgresQuery(ctx context.Context, input RecordManagedPostgresQuery) error {
	if input.ResourceID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 || input.RowCount < 0 || input.DurationMillis < 0 {
		return errors.New("managed PostgreSQL query audit input is incomplete")
	}
	if input.Result != "succeeded" && input.Result != "failed" {
		return errors.New("managed PostgreSQL query audit result is invalid")
	}
	if err := validateMutationActor("access", input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	metadata := map[string]string{
		"actorEmail": input.ActorEmail, "durationMillis": strconv.FormatInt(input.DurationMillis, 10),
		"rowCount": strconv.Itoa(input.RowCount),
	}
	if input.ErrorClass != "" {
		metadata["errorClass"] = input.ErrorClass
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM managed_postgres WHERE id = ? AND project_id = ?)`, input.ResourceID, input.ProjectID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrManagedPostgresNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'postgres.query', 'postgres', ?, ?, ?, ?, ?)`,
			input.AuditEventID, input.ActorID, input.ResourceID, nullableString(input.RequestCorrelationID),
			input.Result, string(metadataJSON), input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit managed PostgreSQL query: %w", err)
		}
		return nil
	})
}

func (store *Store) RecordManagedPostgresExtension(ctx context.Context, input RecordManagedPostgresExtension) error {
	if input.ResourceID == "" || input.ProjectID == "" || input.ExtensionName == "" ||
		input.AuditEventID == "" || input.CreatedAtMillis <= 0 || input.DurationMillis < 0 {
		return errors.New("managed PostgreSQL extension audit input is incomplete")
	}
	if input.Result != "succeeded" && input.Result != "failed" {
		return errors.New("managed PostgreSQL extension audit result is invalid")
	}
	if err := validateMutationActor("access", input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	metadata := map[string]string{
		"actorEmail":     input.ActorEmail,
		"durationMillis": strconv.FormatInt(input.DurationMillis, 10),
		"extension":      input.ExtensionName,
	}
	if input.ErrorClass != "" {
		metadata["errorClass"] = input.ErrorClass
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	action := "postgres.extension.uninstall"
	if input.Install {
		action = "postgres.extension.install"
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM managed_postgres WHERE id = ? AND project_id = ?)`, input.ResourceID, input.ProjectID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrManagedPostgresNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, 'postgres', ?, ?, ?, ?, ?)`,
			input.AuditEventID, input.ActorID, action, input.ResourceID,
			nullableString(input.RequestCorrelationID), input.Result, string(metadataJSON),
			input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit managed PostgreSQL extension: %w", err)
		}
		return nil
	})
}
