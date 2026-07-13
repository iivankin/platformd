package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/iivankin/platformd/internal/bucketname"
	"github.com/iivankin/platformd/internal/corsorigin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/resourcename"
)

var (
	ErrObjectStoreNotFound  = errors.New("object store not found")
	ErrS3CredentialNotFound = errors.New("S3 credential not found")
	ErrObjectNotFound       = errors.New("object not found")
)

type ObjectStore struct {
	ID                   string
	ProjectID            string
	ProjectName          string
	Name                 string
	BucketName           string
	PublicHostname       string
	CORSOrigins          []string
	BackupEnabled        bool
	BackupCron           string
	BackupRetentionCount int
	CreatedAtMillis      int64
	UpdatedAtMillis      int64
}

type S3Credential struct {
	ID               string
	ObjectStoreID    string
	Name             string
	Permission       string
	SecretEncrypted  []byte
	CreatedAtMillis  int64
	LastUsedAtMillis int64
}

type CreateObjectStore struct {
	ID                   string
	ProjectID            string
	Name                 string
	BucketName           string
	PublicHostname       string
	CORSOrigins          []string
	CredentialID         string
	CredentialName       string
	CredentialPermission string
	CredentialSecret     []byte
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) CreateObjectStore(ctx context.Context, input CreateObjectStore) (ObjectStore, S3Credential, error) {
	if input.ID == "" || input.ProjectID == "" || input.CredentialID == "" || input.CredentialName == "" || len(input.CredentialSecret) == 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return ObjectStore{}, S3Credential{}, errors.New("create object store input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	if err := bucketname.Validate(input.BucketName); err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	if input.PublicHostname != "" {
		normalized, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return ObjectStore{}, S3Credential{}, err
		}
		input.PublicHostname = normalized
	}
	if input.CredentialPermission != "read" && input.CredentialPermission != "read_write" {
		return ObjectStore{}, S3Credential{}, errors.New("S3 credential permission must be read or read_write")
	}
	normalizedCORS, err := corsorigin.NormalizeAll(input.CORSOrigins)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	input.CORSOrigins = normalizedCORS
	corsJSON, err := json.Marshal(input.CORSOrigins)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	metadata := make(map[string]string)
	if input.ActorEmail != "" {
		metadata["actorEmail"] = input.ActorEmail
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var projectID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE id = ?", input.ProjectID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return err
		}
		exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, input.Name)
		if err != nil {
			return err
		}
		if exists {
			return ErrResourceNameConflict
		}
		if input.PublicHostname != "" {
			inUse, err := publicHostnameRoleExists(ctx, transaction, input.PublicHostname)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO object_stores(
  id, project_id, name, bucket_name, public_hostname, cors_origins_json,
  created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, input.ID, input.ProjectID, input.Name, input.BucketName,
			nullableString(input.PublicHostname), string(corsJSON), input.CreatedAtMillis, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create object store: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO s3_credentials(id, object_store_id, name, permission, secret_encrypted, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.CredentialID, input.ID, input.CredentialName,
			input.CredentialPermission, input.CredentialSecret, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create initial S3 credential: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'object_store.create', 'object_store', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadataJSON), input.CreatedAtMillis); err != nil {
			return fmt.Errorf("audit object store creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	created, err := store.ObjectStore(ctx, input.ID)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	credential, err := store.S3Credential(ctx, input.CredentialID)
	return created, credential, err
}

func (store *Store) ObjectStore(ctx context.Context, storeID string) (ObjectStore, error) {
	return store.objectStore(ctx, storeID, "")
}

func (store *Store) ObjectStoreByHostname(ctx context.Context, hostname string) (ObjectStore, error) {
	var storeID string
	err := store.database.QueryRowContext(ctx, `
SELECT o.id FROM object_stores o JOIN projects p ON p.id = o.project_id
WHERE o.public_hostname = ? OR (o.name || '.' || p.name || '.internal') = ?`, hostname, hostname).Scan(&storeID)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	if err != nil {
		return ObjectStore{}, fmt.Errorf("load object store by hostname: %w", err)
	}
	return store.ObjectStore(ctx, storeID)
}

func (store *Store) ObjectStoreInProject(ctx context.Context, projectID, storeID string) (ObjectStore, error) {
	if projectID == "" {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	return store.objectStore(ctx, storeID, projectID)
}

func (store *Store) objectStore(ctx context.Context, storeID, projectID string) (ObjectStore, error) {
	var result ObjectStore
	var publicHostname, backupCron sql.NullString
	var corsJSON string
	var backupEnabled int
	query := `
SELECT o.id, o.project_id, p.name, o.name, o.bucket_name, o.public_hostname,
       o.cors_origins_json, o.backup_enabled, o.backup_cron,
       o.backup_retention_count, o.created_at, o.updated_at
FROM object_stores o JOIN projects p ON p.id = o.project_id WHERE o.id = ?`
	arguments := []any{storeID}
	if projectID != "" {
		query += " AND o.project_id = ?"
		arguments = append(arguments, projectID)
	}
	err := store.database.QueryRowContext(ctx, query, arguments...).Scan(
		&result.ID, &result.ProjectID, &result.ProjectName, &result.Name, &result.BucketName,
		&publicHostname, &corsJSON, &backupEnabled, &backupCron,
		&result.BackupRetentionCount, &result.CreatedAtMillis, &result.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	if err != nil {
		return ObjectStore{}, fmt.Errorf("load object store: %w", err)
	}
	if err := json.Unmarshal([]byte(corsJSON), &result.CORSOrigins); err != nil {
		return ObjectStore{}, err
	}
	result.PublicHostname = publicHostname.String
	result.BackupEnabled = backupEnabled == 1
	result.BackupCron = backupCron.String
	return result, nil
}

func (store *Store) ObjectStoresByProject(ctx context.Context, projectID string) ([]ObjectStore, error) {
	var exists int
	if err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", projectID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrProjectNotFound
	}
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM object_stores WHERE project_id = ? ORDER BY name, id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ObjectStore, 0)
	for rows.Next() {
		var storeID string
		if err := rows.Scan(&storeID); err != nil {
			return nil, err
		}
		entry, err := store.ObjectStore(ctx, storeID)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (store *Store) ObjectStores(ctx context.Context) ([]ObjectStore, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM object_stores ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ObjectStore, 0)
	for rows.Next() {
		var storeID string
		if err := rows.Scan(&storeID); err != nil {
			return nil, err
		}
		entry, err := store.ObjectStore(ctx, storeID)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (store *Store) S3Credential(ctx context.Context, credentialID string) (S3Credential, error) {
	var result S3Credential
	var lastUsed sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT id, object_store_id, name, permission, secret_encrypted, created_at, last_used_at
FROM s3_credentials WHERE id = ?`, credentialID).Scan(
		&result.ID, &result.ObjectStoreID, &result.Name, &result.Permission,
		&result.SecretEncrypted, &result.CreatedAtMillis, &lastUsed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return S3Credential{}, ErrS3CredentialNotFound
	}
	if err != nil {
		return S3Credential{}, fmt.Errorf("load S3 credential: %w", err)
	}
	result.LastUsedAtMillis = lastUsed.Int64
	return result, nil
}

type ObjectMetadata struct {
	ObjectStoreID   string
	ObjectKey       string
	PayloadID       string
	ContentType     string
	ETag            string
	Size            int64
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type ObjectPayload struct {
	ID              string
	ObjectStoreID   string
	PlaintextSize   int64
	ChunkCount      int
	PlaintextSHA256 string
	CreatedAtMillis int64
}

type CommitObject struct {
	ObjectStoreID     string
	ObjectKey         string
	Payload           ObjectPayload
	ContentType       string
	ETag              string
	CommittedAtMillis int64
}

func (store *Store) CommitObject(ctx context.Context, input CommitObject) (ObjectMetadata, error) {
	if input.ObjectStoreID == "" || input.ObjectKey == "" || input.Payload.ID == "" || input.Payload.ObjectStoreID != input.ObjectStoreID || input.Payload.PlaintextSize < 0 || input.Payload.ChunkCount < 0 || input.Payload.PlaintextSHA256 == "" || input.ETag == "" || input.CommittedAtMillis <= 0 {
		return ObjectMetadata{}, errors.New("commit object input is invalid")
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM object_stores WHERE id = ?)", input.ObjectStoreID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrObjectStoreNotFound
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO object_payloads(id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.Payload.ID, input.ObjectStoreID, input.Payload.PlaintextSize,
			input.Payload.ChunkCount, input.Payload.PlaintextSHA256, input.CommittedAtMillis); err != nil {
			return fmt.Errorf("create object payload metadata: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO objects(object_store_id, object_key, payload_id, content_type, etag, size, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(object_store_id, object_key) DO UPDATE SET
  payload_id = excluded.payload_id, content_type = excluded.content_type,
  etag = excluded.etag, size = excluded.size, updated_at = excluded.updated_at`,
			input.ObjectStoreID, input.ObjectKey, input.Payload.ID, nullableString(input.ContentType),
			input.ETag, input.Payload.PlaintextSize, input.CommittedAtMillis, input.CommittedAtMillis); err != nil {
			return fmt.Errorf("publish object metadata: %w", err)
		}
		return nil
	})
	if err != nil {
		return ObjectMetadata{}, err
	}
	return store.Object(ctx, input.ObjectStoreID, input.ObjectKey)
}

func (store *Store) Object(ctx context.Context, storeID, objectKey string) (ObjectMetadata, error) {
	var result ObjectMetadata
	var contentType sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT object_store_id, object_key, payload_id, content_type, etag, size, created_at, updated_at
FROM objects WHERE object_store_id = ? AND object_key = ?`, storeID, objectKey).Scan(
		&result.ObjectStoreID, &result.ObjectKey, &result.PayloadID, &contentType,
		&result.ETag, &result.Size, &result.CreatedAtMillis, &result.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectMetadata{}, ErrObjectNotFound
	}
	if err != nil {
		return ObjectMetadata{}, fmt.Errorf("load object: %w", err)
	}
	result.ContentType = contentType.String
	return result, nil
}

func (store *Store) ObjectPayload(ctx context.Context, storeID, payloadID string) (ObjectPayload, error) {
	var result ObjectPayload
	err := store.database.QueryRowContext(ctx, `
SELECT id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at
FROM object_payloads WHERE id = ? AND object_store_id = ?`, payloadID, storeID).Scan(
		&result.ID, &result.ObjectStoreID, &result.PlaintextSize, &result.ChunkCount,
		&result.PlaintextSHA256, &result.CreatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectPayload{}, ErrObjectNotFound
	}
	return result, err
}

func (store *Store) ListObjects(ctx context.Context, storeID, prefix, after string, limit int) ([]ObjectMetadata, bool, error) {
	if limit < 1 || limit > 1000 {
		return nil, false, errors.New("object list limit must be 1..1000")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT object_key, payload_id, content_type, etag, size, created_at, updated_at
FROM objects
WHERE object_store_id = ? AND object_key LIKE ? ESCAPE '\' AND object_key > ?
ORDER BY object_key LIMIT ?`, storeID, escapeLike(prefix)+"%", after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]ObjectMetadata, 0, limit)
	for rows.Next() {
		var item ObjectMetadata
		var contentType sql.NullString
		if err := rows.Scan(&item.ObjectKey, &item.PayloadID, &contentType, &item.ETag, &item.Size, &item.CreatedAtMillis, &item.UpdatedAtMillis); err != nil {
			return nil, false, err
		}
		item.ObjectStoreID = storeID
		item.ContentType = contentType.String
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

func (store *Store) DeleteObject(ctx context.Context, storeID, objectKey string) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, "DELETE FROM objects WHERE object_store_id = ? AND object_key = ?", storeID, objectKey)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrObjectNotFound
		}
		return nil
	})
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
