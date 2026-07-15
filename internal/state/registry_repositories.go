package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/resourcename"
)

var (
	ErrRegistryRepositoryNotFound = errors.New("registry repository not found")
	ErrRegistryCredentialNotFound = errors.New("registry credential not found")
	ErrRegistryNameConflict       = errors.New("registry repository name already exists")
)

type RegistryRepository struct {
	ID                   string
	Name                 string
	PublicPull           bool
	BackupEnabled        bool
	BackupCron           string
	BackupRetentionCount int
	CreatedAtMillis      int64
	UpdatedAtMillis      int64
}

type RegistryCredential struct {
	ID               string
	RepositoryID     string
	Name             string
	Permission       string
	SecretHMAC       []byte
	SecretEncrypted  []byte
	CreatedAtMillis  int64
	LastUsedAtMillis int64
}

type CreateRegistryRepository struct {
	ID                        string
	Name                      string
	PublicPull                bool
	CredentialID              string
	CredentialName            string
	CredentialPermission      string
	CredentialSecretHMAC      []byte
	CredentialSecretEncrypted []byte
	AuditEventID              string
	ActorKind                 string
	ActorID                   string
	ActorEmail                string
	RequestCorrelationID      string
	CreatedAtMillis           int64
}

type SetRegistryRepositoryPublicPull struct {
	RepositoryID         string
	PublicPull           bool
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetRegistryRepositoryPublicPull(ctx context.Context, input SetRegistryRepositoryPublicPull) (RegistryRepository, error) {
	if input.RepositoryID == "" || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return RegistryRepository{}, errors.New("set registry repository public pull input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return RegistryRepository{}, err
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var name string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM registry_repositories WHERE id = ?", input.RepositoryID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
			return ErrRegistryRepositoryNotFound
		} else if err != nil {
			return err
		}
		publicPull := 0
		if input.PublicPull {
			publicPull = 1
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE registry_repositories SET public_pull = ?, updated_at = ? WHERE id = ?`,
			publicPull, input.UpdatedAtMillis, input.RepositoryID); err != nil {
			return err
		}
		metadata, err := json.Marshal(map[string]any{
			"actorEmail": input.ActorEmail, "name": name, "publicPull": input.PublicPull,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'registry.repository.public_pull.set', 'registry_repository', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.RepositoryID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
	if err != nil {
		return RegistryRepository{}, err
	}
	return store.RegistryRepository(ctx, input.RepositoryID)
}

func (store *Store) CreateRegistryRepository(ctx context.Context, input CreateRegistryRepository) (RegistryRepository, RegistryCredential, error) {
	if input.ID == "" || input.CredentialID == "" || len(input.CredentialSecretHMAC) != 32 || len(input.CredentialSecretEncrypted) == 0 || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return RegistryRepository{}, RegistryCredential{}, errors.New("create registry repository input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	if err := registryname.ValidateRepository(input.Name); err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	if err := resourcename.Validate(input.CredentialName); err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	if input.CredentialPermission != "pull" && input.CredentialPermission != "pull_push" {
		return RegistryRepository{}, RegistryCredential{}, errors.New("registry credential permission must be pull or pull_push")
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": input.ActorEmail, "name": input.Name, "publicPull": input.PublicPull,
	})
	if err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var existing string
		err := transaction.QueryRowContext(ctx, "SELECT id FROM registry_repositories WHERE name = ?", input.Name).Scan(&existing)
		if err == nil {
			return ErrRegistryNameConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		publicPull := 0
		if input.PublicPull {
			publicPull = 1
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_repositories(id, name, public_pull, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, input.ID, input.Name, publicPull, input.CreatedAtMillis, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create registry repository: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO registry_credentials(id, repository_id, name, permission, secret_hmac, secret_encrypted, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, input.CredentialID, input.ID, input.CredentialName,
			input.CredentialPermission, input.CredentialSecretHMAC, input.CredentialSecretEncrypted, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create initial registry credential: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, 'registry.repository.create', 'registry_repository', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorKind, input.ActorID, input.ID,
			nullableString(input.RequestCorrelationID), string(metadata), input.CreatedAtMillis); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	repository, err := store.RegistryRepository(ctx, input.ID)
	if err != nil {
		return RegistryRepository{}, RegistryCredential{}, err
	}
	credential, err := store.RegistryCredential(ctx, input.CredentialID)
	return repository, credential, err
}

func (store *Store) RegistryRepository(ctx context.Context, repositoryID string) (RegistryRepository, error) {
	return store.registryRepository(ctx, "id", repositoryID)
}

func (store *Store) RegistryRepositoryByName(ctx context.Context, name string) (RegistryRepository, error) {
	return store.registryRepository(ctx, "name", name)
}

func (store *Store) registryRepository(ctx context.Context, column, value string) (RegistryRepository, error) {
	if column != "id" && column != "name" {
		return RegistryRepository{}, errors.New("invalid registry lookup column")
	}
	var result RegistryRepository
	var publicPull, backupEnabled int
	var backupCron sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT id, name, public_pull, backup_enabled, backup_cron,
       backup_retention_count, created_at, updated_at
FROM registry_repositories WHERE `+column+` = ?`, value).Scan(
		&result.ID, &result.Name, &publicPull, &backupEnabled, &backupCron,
		&result.BackupRetentionCount, &result.CreatedAtMillis, &result.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistryRepository{}, ErrRegistryRepositoryNotFound
	}
	if err != nil {
		return RegistryRepository{}, err
	}
	result.PublicPull = publicPull == 1
	result.BackupEnabled = backupEnabled == 1
	result.BackupCron = backupCron.String
	return result, nil
}

func (store *Store) RegistryRepositories(ctx context.Context) ([]RegistryRepository, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT id FROM registry_repositories ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RegistryRepository, 0)
	for rows.Next() {
		var repositoryID string
		if err := rows.Scan(&repositoryID); err != nil {
			return nil, err
		}
		repository, err := store.RegistryRepository(ctx, repositoryID)
		if err != nil {
			return nil, err
		}
		result = append(result, repository)
	}
	return result, rows.Err()
}

func (store *Store) RegistryCredential(ctx context.Context, credentialID string) (RegistryCredential, error) {
	var result RegistryCredential
	var lastUsed sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT id, repository_id, name, permission, secret_hmac, secret_encrypted, created_at, last_used_at
FROM registry_credentials WHERE id = ?`, credentialID).Scan(
		&result.ID, &result.RepositoryID, &result.Name, &result.Permission,
		&result.SecretHMAC, &result.SecretEncrypted, &result.CreatedAtMillis, &lastUsed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistryCredential{}, ErrRegistryCredentialNotFound
	}
	if err != nil {
		return RegistryCredential{}, err
	}
	result.LastUsedAtMillis = lastUsed.Int64
	return result, nil
}
