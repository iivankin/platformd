package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ServiceImageCredential struct {
	ServiceID         string
	RegistryHost      string
	Username          string
	PasswordEncrypted []byte
	UpdatedAtMillis   int64
}

func (store *Store) ServiceImageCredential(ctx context.Context, serviceID string) (ServiceImageCredential, error) {
	var credential ServiceImageCredential
	err := store.database.QueryRowContext(ctx, `
SELECT service_id, registry_host, username, password_encrypted, updated_at
FROM service_image_credentials WHERE service_id = ?`, serviceID).Scan(
		&credential.ServiceID, &credential.RegistryHost, &credential.Username,
		&credential.PasswordEncrypted, &credential.UpdatedAtMillis,
	)
	if err != nil {
		return ServiceImageCredential{}, err
	}
	return credential, nil
}

func replaceServiceImageCredential(ctx context.Context, transaction *sql.Tx, credential ServiceImageCredential) error {
	if credential.ServiceID == "" || credential.RegistryHost == "" || credential.Username == "" || len(credential.PasswordEncrypted) == 0 || credential.UpdatedAtMillis <= 0 {
		return errors.New("service image credential is incomplete")
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_image_credentials(service_id, registry_host, username, password_encrypted, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(service_id) DO UPDATE SET
  registry_host = excluded.registry_host,
  username = excluded.username,
  password_encrypted = excluded.password_encrypted,
  updated_at = excluded.updated_at`,
		credential.ServiceID, credential.RegistryHost, credential.Username,
		credential.PasswordEncrypted, credential.UpdatedAtMillis,
	); err != nil {
		return fmt.Errorf("replace service image credential: %w", err)
	}
	return nil
}
