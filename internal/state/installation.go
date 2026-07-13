package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrAlreadyInitialized = errors.New("installation is already initialized")
	ErrNotInitialized     = errors.New("installation is not initialized")
)

type InitialInstallation struct {
	ID                   string
	AdminHostname        string
	AutomationHostname   *string
	RegistryHostname     *string
	AccessTeamDomain     string
	AccessAudience       string
	ConsolePassphrasePHC string
	OriginCertificateID  string
	OriginCertificatePEM string
	OriginPrivateKey     []byte
	InitialAuditEventID  string
	CreatedAtMillis      int64
}

type Installation struct {
	ID                   string
	AdminHostname        string
	AutomationHostname   *string
	RegistryHostname     *string
	AccessTeamDomain     string
	AccessAudience       string
	ConsolePassphrasePHC string
	RecoveryMode         bool
	CreatedAtMillis      int64
	UpdatedAtMillis      int64
	OriginCertificates   []OriginCertificate
}

type OriginCertificate struct {
	ID                  string
	CertificatePEM      string
	PrivateKeyEncrypted []byte
	CreatedAtMillis     int64
}

type ResetConsolePassphrase struct {
	Verifier      string
	AuditEventID  string
	ResetAtMillis int64
}

func (store *Store) CreateInstallation(ctx context.Context, input InitialInstallation) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var count int
		if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM installation").Scan(&count); err != nil {
			return fmt.Errorf("check installation state: %w", err)
		}
		if count != 0 {
			return ErrAlreadyInitialized
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO origin_certificates(id, certificate_pem, private_key_encrypted, created_at)
VALUES (?, ?, ?, ?)`, input.OriginCertificateID, input.OriginCertificatePEM, input.OriginPrivateKey, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create initial Origin certificate: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO installation(
  singleton, id, admin_hostname, automation_hostname, registry_hostname,
  access_team_domain, access_audience,
  console_passphrase_phc, recovery_mode, created_at, updated_at
) VALUES (1, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			input.ID,
			input.AdminHostname,
			input.AutomationHostname,
			input.RegistryHostname,
			input.AccessTeamDomain,
			input.AccessAudience,
			input.ConsolePassphrasePHC,
			input.CreatedAtMillis,
			input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create installation: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'local_root', 'init', 'installation.create', 'installation', ?, 'succeeded', '{}', ?)`,
			input.InitialAuditEventID,
			input.ID,
			input.CreatedAtMillis,
		); err != nil {
			return fmt.Errorf("create initial audit event: %w", err)
		}
		return nil
	})
}

func (store *Store) Installation(ctx context.Context) (Installation, error) {
	var installation Installation
	var automationHostname, registryHostname sql.NullString
	var recoveryMode int
	err := store.database.QueryRowContext(ctx, `
SELECT id, admin_hostname, automation_hostname, registry_hostname, access_team_domain,
       access_audience, console_passphrase_phc,
       recovery_mode, created_at, updated_at
FROM installation
WHERE singleton = 1`).Scan(
		&installation.ID,
		&installation.AdminHostname,
		&automationHostname,
		&registryHostname,
		&installation.AccessTeamDomain,
		&installation.AccessAudience,
		&installation.ConsolePassphrasePHC,
		&recoveryMode,
		&installation.CreatedAtMillis,
		&installation.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Installation{}, ErrNotInitialized
	}
	if err != nil {
		return Installation{}, fmt.Errorf("read installation: %w", err)
	}
	if automationHostname.Valid {
		installation.AutomationHostname = &automationHostname.String
	}
	if registryHostname.Valid {
		installation.RegistryHostname = &registryHostname.String
	}
	installation.RecoveryMode = recoveryMode == 1

	rows, err := store.database.QueryContext(ctx, `
SELECT id, certificate_pem, private_key_encrypted, created_at
FROM origin_certificates
ORDER BY created_at, id`)
	if err != nil {
		return Installation{}, fmt.Errorf("list Origin certificates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var certificate OriginCertificate
		if err := rows.Scan(&certificate.ID, &certificate.CertificatePEM, &certificate.PrivateKeyEncrypted, &certificate.CreatedAtMillis); err != nil {
			return Installation{}, fmt.Errorf("scan Origin certificate: %w", err)
		}
		installation.OriginCertificates = append(installation.OriginCertificates, certificate)
	}
	if err := rows.Err(); err != nil {
		return Installation{}, fmt.Errorf("iterate Origin certificates: %w", err)
	}
	return installation, nil
}

func (store *Store) ResetConsolePassphrase(ctx context.Context, input ResetConsolePassphrase) error {
	if input.Verifier == "" || input.AuditEventID == "" || input.ResetAtMillis <= 0 {
		return errors.New("console passphrase reset input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return fmt.Errorf("read installation for console passphrase reset: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE installation
SET console_passphrase_phc = ?, updated_at = ?
WHERE singleton = 1`, input.Verifier, input.ResetAtMillis)
		if err != nil {
			return fmt.Errorf("reset console passphrase verifier: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("reset console passphrase changed %d installation rows: %w", changed, err)
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'local_root', 'init', 'installation.console_passphrase_reset', 'installation', ?, 'succeeded', '{}', ?)`,
			input.AuditEventID, installationID, input.ResetAtMillis,
		)
		if err != nil {
			return fmt.Errorf("record console passphrase reset audit: %w", err)
		}
		return nil
	})
}
