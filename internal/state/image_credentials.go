package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/resourcename"
)

var ErrImageCredentialNameConflict = errors.New("image registry credential name already exists")

type ImageRegistryCredential struct {
	ID                string
	ProjectID         string
	Name              string
	RegistryHost      string
	Username          string
	PasswordEncrypted []byte
	CreatedAtMillis   int64
}

type CreateImageRegistryCredential struct {
	ImageRegistryCredential
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
}

func (store *Store) CreateImageRegistryCredential(ctx context.Context, input CreateImageRegistryCredential) (ImageRegistryCredential, error) {
	credential := input.ImageRegistryCredential
	if credential.ID == "" || credential.ProjectID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || credential.CreatedAtMillis <= 0 || len(credential.PasswordEncrypted) == 0 {
		return ImageRegistryCredential{}, errors.New("create image registry credential input is incomplete")
	}
	if err := resourcename.Validate(credential.Name); err != nil {
		return ImageRegistryCredential{}, err
	}
	host, err := imagecredential.NormalizeHost(credential.RegistryHost)
	if err != nil {
		return ImageRegistryCredential{}, err
	}
	credential.RegistryHost = host
	if err := imagecredential.ValidateUsername(credential.Username); err != nil {
		return ImageRegistryCredential{}, err
	}
	metadata, err := json.Marshal(map[string]string{
		"actorEmail":   input.ActorEmail,
		"registryHost": credential.RegistryHost,
	})
	if err != nil {
		return ImageRegistryCredential{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var projectID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM projects WHERE id = ?", credential.ProjectID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		} else if err != nil {
			return fmt.Errorf("load image credential project: %w", err)
		}
		var existingID string
		err := transaction.QueryRowContext(ctx, `
SELECT id FROM image_registry_credentials WHERE project_id = ? AND name = ?`, credential.ProjectID, credential.Name).Scan(&existingID)
		if err == nil {
			return ErrImageCredentialNameConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check image credential name: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO image_registry_credentials(
  id, project_id, name, registry_host, username, password_encrypted, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`, credential.ID, credential.ProjectID, credential.Name,
			credential.RegistryHost, credential.Username, credential.PasswordEncrypted, credential.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create image registry credential: %w", err)
		}
		var correlationID any
		if input.RequestCorrelationID != "" {
			correlationID = input.RequestCorrelationID
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'image_credential.create', 'image_credential', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, credential.ID, correlationID, string(metadata), credential.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("audit image credential creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return ImageRegistryCredential{}, err
	}
	return credential, nil
}

func (store *Store) ImageRegistryCredential(ctx context.Context, credentialID string) (ImageRegistryCredential, error) {
	var credential ImageRegistryCredential
	err := store.database.QueryRowContext(ctx, `
SELECT id, project_id, name, registry_host, username, password_encrypted, created_at
FROM image_registry_credentials WHERE id = ?`, credentialID).Scan(
		&credential.ID, &credential.ProjectID, &credential.Name, &credential.RegistryHost,
		&credential.Username, &credential.PasswordEncrypted, &credential.CreatedAtMillis,
	)
	if err != nil {
		return ImageRegistryCredential{}, err
	}
	return credential, nil
}

func (store *Store) ImageRegistryCredentials(ctx context.Context, projectID string) ([]ImageRegistryCredential, error) {
	var exists int
	if err := store.database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)", projectID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check image credential project: %w", err)
	}
	if exists != 1 {
		return nil, ErrProjectNotFound
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, name, registry_host, username, created_at
FROM image_registry_credentials WHERE project_id = ? ORDER BY name, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list image registry credentials: %w", err)
	}
	defer rows.Close()
	credentials := make([]ImageRegistryCredential, 0)
	for rows.Next() {
		var credential ImageRegistryCredential
		if err := rows.Scan(
			&credential.ID, &credential.ProjectID, &credential.Name, &credential.RegistryHost,
			&credential.Username, &credential.CreatedAtMillis,
		); err != nil {
			return nil, fmt.Errorf("scan image registry credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image registry credentials: %w", err)
	}
	return credentials, nil
}
