package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type DeleteResourceInput struct {
	ID                    string
	ProjectID             string
	ExpectedUpdatedMillis int64
	AuditEventID          string
	ActorKind             string
	ActorID               string
	ActorEmail            string
	RequestCorrelationID  string
	DeletedAtMillis       int64
}

func (store *Store) DeleteManagedPostgres(ctx context.Context, input DeleteResourceInput) (ManagedPostgres, error) {
	if err := validateResourceDeletion(input); err != nil {
		return ManagedPostgres{}, err
	}
	resource, err := store.ManagedPostgresInProject(ctx, input.ProjectID, input.ID)
	if err != nil {
		return ManagedPostgres{}, err
	}
	err = store.deleteResource(ctx, input, resource.Name, "managed_postgres", "postgres", ErrManagedPostgresChanged)
	if err != nil {
		return ManagedPostgres{}, err
	}
	return resource, nil
}

func (store *Store) DeleteManagedRedis(ctx context.Context, input DeleteResourceInput) (ManagedRedis, error) {
	if err := validateResourceDeletion(input); err != nil {
		return ManagedRedis{}, err
	}
	resource, err := store.ManagedRedisInProject(ctx, input.ProjectID, input.ID)
	if err != nil {
		return ManagedRedis{}, err
	}
	err = store.deleteResource(ctx, input, resource.Name, "managed_redis", "redis", ErrManagedRedisChanged)
	if err != nil {
		return ManagedRedis{}, err
	}
	return resource, nil
}

func (store *Store) DeleteObjectStore(ctx context.Context, input DeleteResourceInput) (ObjectStore, error) {
	if err := validateResourceDeletion(input); err != nil {
		return ObjectStore{}, err
	}
	resource, err := store.ObjectStoreInProject(ctx, input.ProjectID, input.ID)
	if err != nil {
		return ObjectStore{}, err
	}
	err = store.deleteResource(ctx, input, resource.Name, "object_stores", "object_store", ErrObjectStoreChanged)
	if err != nil {
		return ObjectStore{}, err
	}
	return resource, nil
}

func validateResourceDeletion(input DeleteResourceInput) error {
	if input.ID == "" || input.ProjectID == "" || input.ExpectedUpdatedMillis <= 0 ||
		input.AuditEventID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete resource input is incomplete")
	}
	return validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail)
}

func (store *Store) deleteResource(
	ctx context.Context,
	input DeleteResourceInput,
	name, table, kind string,
	changedError error,
) error {
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": input.ActorEmail, "name": name, "backupsRetained": true,
	})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if kind == "postgres" || kind == "redis" {
			if _, err := transaction.ExecContext(ctx,
				"DELETE FROM runtime_deployments WHERE resource_kind = ? AND resource_id = ?",
				kind, input.ID,
			); err != nil {
				return fmt.Errorf("delete %s deployment history: %w", kind, err)
			}
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM operations WHERE target_id = ?", input.ID); err != nil {
			return fmt.Errorf("delete %s operations: %w", kind, err)
		}
		result, err := transaction.ExecContext(ctx,
			"DELETE FROM "+table+" WHERE id = ? AND project_id = ? AND updated_at = ?",
			input.ID, input.ProjectID, input.ExpectedUpdatedMillis,
		)
		if err != nil {
			return fmt.Errorf("delete %s resource: %w", kind, err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted %s resources: %w", kind, err)
		}
		if changed != 1 {
			return changedError
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ProjectID, input.ActorKind, input.ActorID,
			kind+".delete", kind, input.ID, nullableString(input.RequestCorrelationID),
			string(metadata), input.DeletedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("audit %s deletion: %w", kind, err)
		}
		return nil
	})
}
