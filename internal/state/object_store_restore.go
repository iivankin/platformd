package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type RestoreObjectStore struct {
	ObjectStoreID        string
	Payloads             []ObjectPayload
	Objects              []ObjectMetadata
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) RestoreObjectStore(ctx context.Context, input RestoreObjectStore) error {
	if err := validateRestoreObjectStore(input); err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": input.ActorEmail, "objectCount": len(input.Objects), "payloadCount": len(input.Payloads),
	})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM object_stores WHERE id = ?)", input.ObjectStoreID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrObjectStoreNotFound
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM objects WHERE object_store_id = ?", input.ObjectStoreID); err != nil {
			return fmt.Errorf("remove current object metadata: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM object_payloads WHERE object_store_id = ?", input.ObjectStoreID); err != nil {
			return fmt.Errorf("remove current object payload metadata: %w", err)
		}
		for _, payload := range input.Payloads {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO object_payloads(id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, payload.ID, input.ObjectStoreID, payload.PlaintextSize,
				payload.ChunkCount, payload.PlaintextSHA256, payload.CreatedAtMillis); err != nil {
				return fmt.Errorf("restore object payload metadata: %w", err)
			}
		}
		for _, object := range input.Objects {
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO objects(object_store_id, object_key, payload_id, content_type, etag, size, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, input.ObjectStoreID, object.ObjectKey, object.PayloadID,
				nullableString(object.ContentType), object.ETag, object.Size,
				object.CreatedAtMillis, object.UpdatedAtMillis); err != nil {
				return fmt.Errorf("restore object metadata: %w", err)
			}
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'object_store.restore', 'object_store', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ObjectStoreID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis)
		return err
	})
}

func validateRestoreObjectStore(input RestoreObjectStore) error {
	if input.ObjectStoreID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("restore object store input is incomplete")
	}
	if input.ActorKind == "system" {
		if input.ActorID == "" || input.ActorEmail != "" {
			return errors.New("system restore actor is invalid")
		}
	} else if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	payloads := make(map[string]ObjectPayload, len(input.Payloads))
	for _, payload := range input.Payloads {
		if payload.ID == "" || payload.ObjectStoreID != input.ObjectStoreID || payload.PlaintextSize < 0 ||
			payload.ChunkCount < 0 || !validLowerSHA256(payload.PlaintextSHA256) || payload.CreatedAtMillis <= 0 {
			return errors.New("restore object payload metadata is invalid")
		}
		if _, exists := payloads[payload.ID]; exists {
			return errors.New("restore object payload metadata contains duplicate IDs")
		}
		payloads[payload.ID] = payload
	}
	objects := make(map[string]struct{}, len(input.Objects))
	for _, object := range input.Objects {
		payload, exists := payloads[object.PayloadID]
		if object.ObjectStoreID != input.ObjectStoreID || !validRestoredObjectKey(object.ObjectKey) ||
			!exists || object.ETag == "" || object.Size != payload.PlaintextSize ||
			object.CreatedAtMillis <= 0 || object.UpdatedAtMillis < object.CreatedAtMillis ||
			!utf8.ValidString(object.ContentType) || strings.ContainsRune(object.ContentType, 0) {
			return errors.New("restore object metadata is invalid")
		}
		if _, exists := objects[object.ObjectKey]; exists {
			return errors.New("restore object metadata contains duplicate keys")
		}
		objects[object.ObjectKey] = struct{}{}
	}
	return nil
}

func validRestoredObjectKey(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
