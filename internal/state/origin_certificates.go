package state

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type hostnameQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

var ErrOriginCertificateNotFound = errors.New("Origin certificate not found")

type OriginCertificateCoverageError struct {
	Hostnames []string
}

func (failure *OriginCertificateCoverageError) Error() string {
	return fmt.Sprintf("no configured Origin certificate covers: %s", strings.Join(failure.Hostnames, ", "))
}

func (failure *OriginCertificateCoverageError) Unwrap() error { return ErrCertificateCoverage }

type PutOriginCertificateInput struct {
	Certificate          OriginCertificate
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

type DeleteOriginCertificateInput struct {
	CertificateID        string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) AddOriginCertificate(ctx context.Context, input PutOriginCertificateInput) error {
	return store.putOriginCertificate(ctx, input, false)
}

func (store *Store) ReplaceOriginCertificate(ctx context.Context, input PutOriginCertificateInput) error {
	return store.putOriginCertificate(ctx, input, true)
}

func (store *Store) putOriginCertificate(ctx context.Context, input PutOriginCertificateInput, replace bool) error {
	certificate := input.Certificate
	if certificate.ID == "" || certificate.CertificatePEM == "" || len(certificate.PrivateKeyEncrypted) == 0 ||
		certificate.CreatedAtMillis <= 0 || input.AuditEventID == "" || input.ActorID == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("Origin certificate mutation input is incomplete")
	}
	if _, err := originCertificateLeaf(certificate.CertificatePEM); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		action := "installation.origin_certificate.add"
		if replace {
			action = "installation.origin_certificate.replace"
			result, err := transaction.ExecContext(ctx, `
UPDATE origin_certificates
SET certificate_pem = ?, private_key_encrypted = ?, created_at = ?
WHERE id = ?`, certificate.CertificatePEM, certificate.PrivateKeyEncrypted, certificate.CreatedAtMillis, certificate.ID)
			if err != nil {
				return fmt.Errorf("replace Origin certificate: %w", err)
			}
			changed, err := result.RowsAffected()
			if err != nil || changed != 1 {
				return ErrOriginCertificateNotFound
			}
		} else if _, err := transaction.ExecContext(ctx, `
INSERT INTO origin_certificates(id, certificate_pem, private_key_encrypted, created_at)
VALUES (?, ?, ?, ?)`, certificate.ID, certificate.CertificatePEM, certificate.PrivateKeyEncrypted, certificate.CreatedAtMillis); err != nil {
			return fmt.Errorf("add Origin certificate: %w", err)
		}
		if err := validateOriginCertificateCoverage(ctx, transaction); err != nil {
			return err
		}
		return recordOriginCertificateMutation(ctx, transaction, installationID, certificate.ID, action,
			input.AuditEventID, input.ActorID, input.ActorEmail, input.RequestCorrelationID, input.UpdatedAtMillis)
	})
}

func (store *Store) DeleteOriginCertificate(ctx context.Context, input DeleteOriginCertificateInput) error {
	if input.CertificateID == "" || input.AuditEventID == "" || input.ActorID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete Origin certificate input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID string
		if err := transaction.QueryRowContext(ctx, "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM origin_certificates WHERE id = ?", input.CertificateID)
		if err != nil {
			return fmt.Errorf("delete Origin certificate: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrOriginCertificateNotFound
		}
		if err := validateOriginCertificateCoverage(ctx, transaction); err != nil {
			return err
		}
		return recordOriginCertificateMutation(ctx, transaction, installationID, input.CertificateID,
			"installation.origin_certificate.delete", input.AuditEventID, input.ActorID, input.ActorEmail,
			input.RequestCorrelationID, input.DeletedAtMillis)
	})
}

func recordOriginCertificateMutation(
	ctx context.Context,
	transaction *sql.Tx,
	installationID, certificateID, action, auditEventID, actorID, actorEmail, correlationID string,
	timestamp int64,
) error {
	if _, err := transaction.ExecContext(ctx, "UPDATE installation SET updated_at = ? WHERE singleton = 1", timestamp); err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]string{"actorEmail": actorEmail, "certificateId": certificateID})
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, 'installation', ?, ?, 'succeeded', ?, ?)`,
		auditEventID, actorID, action, installationID, nullableString(correlationID), string(metadata), timestamp)
	return err
}

func validateOriginCertificateCoverage(ctx context.Context, transaction *sql.Tx) error {
	hostnames, err := publicHostnames(ctx, transaction)
	if err != nil {
		return err
	}
	uncovered := make([]string, 0)
	for _, hostname := range hostnames {
		covered, err := originCertificateCoversHostname(ctx, transaction, hostname)
		if err != nil {
			return err
		}
		if !covered {
			uncovered = append(uncovered, hostname)
		}
	}
	if len(uncovered) != 0 {
		return &OriginCertificateCoverageError{Hostnames: uncovered}
	}
	return nil
}

func originCertificateCoversHostname(ctx context.Context, transaction *sql.Tx, hostname string) (bool, error) {
	rows, err := transaction.QueryContext(ctx, "SELECT certificate_pem FROM origin_certificates ORDER BY id")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var certificatePEM string
		if err := rows.Scan(&certificatePEM); err != nil {
			return false, err
		}
		leaf, err := originCertificateLeaf(certificatePEM)
		if err != nil {
			return false, err
		}
		if leaf.VerifyHostname(hostname) == nil {
			return true, nil
		}
	}
	return false, rows.Err()
}

func originCertificateLeaf(certificatePEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("Origin certificate PEM has no leaf certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Origin certificate leaf: %w", err)
	}
	return leaf, nil
}

func (store *Store) PublicHostnames(ctx context.Context) ([]string, error) {
	return queryPublicHostnames(ctx, store.database)
}

func publicHostnames(ctx context.Context, transaction *sql.Tx) ([]string, error) {
	return queryPublicHostnames(ctx, transaction)
}

func queryPublicHostnames(ctx context.Context, querier hostnameQuerier) ([]string, error) {
	rows, err := querier.QueryContext(ctx, `
SELECT admin_hostname FROM installation
UNION SELECT automation_hostname FROM installation WHERE automation_hostname IS NOT NULL
UNION SELECT registry_hostname FROM installation WHERE registry_hostname IS NOT NULL
UNION SELECT hostname FROM service_domains
UNION SELECT public_hostname FROM object_stores WHERE public_hostname IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hostnames := make([]string, 0)
	for rows.Next() {
		var hostname string
		if err := rows.Scan(&hostname); err != nil {
			return nil, err
		}
		hostnames = append(hostnames, hostname)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(hostnames)
	return hostnames, nil
}
