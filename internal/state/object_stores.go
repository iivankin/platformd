package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/bucketname"
	"github.com/iivankin/platformd/internal/corsorigin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/resourcename"
)

var (
	ErrObjectStoreNotFound  = errors.New("object store not found")
	ErrS3CredentialNotFound = errors.New("S3 credential not found")
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
	BackupPolicy         InitialBackupPolicy
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
	backupPolicy, err := normalizeInitialBackupPolicy(input.BackupPolicy)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	corsJSON, err := json.Marshal(input.CORSOrigins)
	if err != nil {
		return ObjectStore{}, S3Credential{}, err
	}
	metadata := map[string]string{"name": input.Name}
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
		if err := validateInitialBackupTarget(ctx, transaction, backupPolicy.TargetID); err != nil {
			return err
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
  backup_target_id, backup_enabled, backup_cron, backup_retention_count,
  created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.ID, input.ProjectID, input.Name, input.BucketName,
			nullableString(input.PublicHostname), string(corsJSON), nullableString(backupPolicy.TargetID),
			boolInteger(backupPolicy.Enabled), nullableString(backupPolicy.Cron), backupPolicy.RetentionCount,
			input.CreatedAtMillis, input.CreatedAtMillis); err != nil {
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
  id, project_id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'object_store.create', 'object_store', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ProjectID, input.ActorKind, input.ActorID, input.ID,
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

const objectStoreSelect = `
SELECT o.id, o.project_id, p.name, o.name, o.bucket_name, o.public_hostname,
       o.cors_origins_json, o.backup_enabled, o.backup_cron,
       o.backup_retention_count, o.created_at, o.updated_at
FROM object_stores o JOIN projects p ON p.id = o.project_id`

func (store *Store) ObjectStoreByHostname(ctx context.Context, hostname string) (ObjectStore, error) {
	result, err := scanObjectStore(store.database.QueryRowContext(
		ctx, objectStoreSelect+" WHERE o.public_hostname = ?", hostname,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	if err != nil {
		return ObjectStore{}, fmt.Errorf("load object store by hostname: %w", err)
	}
	return result, nil
}

func (store *Store) ObjectStoreInProject(ctx context.Context, projectID, storeID string) (ObjectStore, error) {
	if projectID == "" {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	return store.objectStore(ctx, storeID, projectID)
}

func (store *Store) objectStore(ctx context.Context, storeID, projectID string) (ObjectStore, error) {
	query := objectStoreSelect + " WHERE o.id = ?"
	arguments := []any{storeID}
	if projectID != "" {
		query += " AND o.project_id = ?"
		arguments = append(arguments, projectID)
	}
	result, err := scanObjectStore(store.database.QueryRowContext(ctx, query, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectStore{}, ErrObjectStoreNotFound
	}
	if err != nil {
		return ObjectStore{}, fmt.Errorf("load object store: %w", err)
	}
	return result, nil
}

type objectStoreScanner interface {
	Scan(...any) error
}

func scanObjectStore(scanner objectStoreScanner) (ObjectStore, error) {
	var result ObjectStore
	var publicHostname, backupCron sql.NullString
	var corsJSON string
	var backupEnabled int
	err := scanner.Scan(
		&result.ID, &result.ProjectID, &result.ProjectName, &result.Name, &result.BucketName,
		&publicHostname, &corsJSON, &backupEnabled, &backupCron,
		&result.BackupRetentionCount, &result.CreatedAtMillis, &result.UpdatedAtMillis,
	)
	if err != nil {
		return ObjectStore{}, err
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
	rows, err := store.database.QueryContext(
		ctx, objectStoreSelect+" WHERE o.project_id = ? ORDER BY o.name, o.id", projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ObjectStore, 0)
	for rows.Next() {
		entry, err := scanObjectStore(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (store *Store) ObjectStores(ctx context.Context) ([]ObjectStore, error) {
	rows, err := store.database.QueryContext(ctx, objectStoreSelect+" ORDER BY o.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ObjectStore, 0)
	for rows.Next() {
		entry, err := scanObjectStore(rows)
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

func (store *Store) S3CredentialsByObjectStore(ctx context.Context, objectStoreID string) ([]S3Credential, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id FROM s3_credentials WHERE object_store_id = ? ORDER BY created_at, id`, objectStoreID)
	if err != nil {
		return nil, fmt.Errorf("list S3 credentials: %w", err)
	}
	defer rows.Close()
	credentials := make([]S3Credential, 0)
	for rows.Next() {
		var credentialID string
		if err := rows.Scan(&credentialID); err != nil {
			return nil, err
		}
		credential, err := store.S3Credential(ctx, credentialID)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}
