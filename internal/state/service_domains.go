package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/publichostname"
)

var (
	ErrDomainNotFound          = errors.New("service domain not found")
	ErrHostnameInUse           = errors.New("hostname is already used by another public role")
	ErrServiceTargetPortNeeded = errors.New("service target port is required for a public domain")
	ErrCertificateCoverage     = errors.New("no configured Origin certificate covers this hostname")
)

type ServiceDomain struct {
	Hostname    string
	ServiceID   string
	ServiceName string
	ProjectID   string
	ProjectName string
	CreatedAt   int64
}

type DomainConflict struct {
	Domain ServiceDomain
}

func (conflict *DomainConflict) Error() string {
	return fmt.Sprintf("hostname %s belongs to service %s in project %s", conflict.Domain.Hostname, conflict.Domain.ServiceName, conflict.Domain.ProjectName)
}

type AttachServiceDomainInput struct {
	ProjectID            string
	ServiceID            string
	Hostname             string
	Move                 bool
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type DetachServiceDomainInput struct {
	ProjectID            string
	ServiceID            string
	Hostname             string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) ServiceDomains(ctx context.Context, projectID, serviceID string) ([]ServiceDomain, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT d.hostname, d.service_id, s.name, s.project_id, p.name, d.created_at
FROM service_domains d
JOIN services s ON s.id = d.service_id
JOIN projects p ON p.id = s.project_id
WHERE d.service_id = ?
ORDER BY d.hostname`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service domains: %w", err)
	}
	defer rows.Close()
	return scanServiceDomains(rows)
}

func (store *Store) ApplicationDomains(ctx context.Context) ([]ServiceDomain, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT d.hostname, d.service_id, s.name, s.project_id, p.name, d.created_at
FROM service_domains d
JOIN services s ON s.id = d.service_id
JOIN projects p ON p.id = s.project_id
ORDER BY d.hostname`)
	if err != nil {
		return nil, fmt.Errorf("list application domains: %w", err)
	}
	defer rows.Close()
	return scanServiceDomains(rows)
}

func (store *Store) AttachServiceDomain(ctx context.Context, input AttachServiceDomainInput) (ServiceDomain, error) {
	if input.ProjectID == "" || input.ServiceID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.CreatedAtMillis <= 0 {
		return ServiceDomain{}, errors.New("attach service domain input is incomplete")
	}
	hostname, err := publichostname.Normalize(input.Hostname)
	if err != nil {
		return ServiceDomain{}, err
	}
	var route ServiceDomain
	err = store.Write(ctx, func(transaction *sql.Tx) error {
		target, err := loadDomainTarget(ctx, transaction, input.ProjectID, input.ServiceID)
		if err != nil {
			return err
		}
		existing, exists, err := loadServiceDomain(ctx, transaction, hostname)
		if err != nil {
			return err
		}
		action := "service.domain.attach"
		metadata := map[string]string{"hostname": hostname}
		createdAt := input.CreatedAtMillis
		if exists && existing.ServiceID != input.ServiceID {
			if !input.Move {
				return &DomainConflict{Domain: existing}
			}
			action = "service.domain.move"
			metadata["sourceProjectId"] = existing.ProjectID
			metadata["sourceServiceId"] = existing.ServiceID
			if _, err := transaction.ExecContext(ctx, `
UPDATE service_domains SET service_id = ?, created_at = ? WHERE hostname = ?`,
				input.ServiceID, input.CreatedAtMillis, hostname,
			); err != nil {
				return fmt.Errorf("move service domain: %w", err)
			}
		} else if !exists {
			inUse, err := publicHostnameRoleExists(ctx, transaction, hostname)
			if err != nil {
				return err
			}
			if inUse {
				return ErrHostnameInUse
			}
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_domains(hostname, service_id, created_at) VALUES (?, ?, ?)`,
				hostname, input.ServiceID, input.CreatedAtMillis,
			); err != nil {
				return fmt.Errorf("attach service domain: %w", err)
			}
		} else {
			createdAt = existing.CreatedAt
		}
		route = target
		route.Hostname = hostname
		route.CreatedAt = createdAt
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: action, ServiceID: input.ServiceID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
			Metadata: metadata,
		})
	})
	if err != nil {
		return ServiceDomain{}, err
	}
	return route, nil
}

func (store *Store) DetachServiceDomain(ctx context.Context, input DetachServiceDomainInput) error {
	if input.ProjectID == "" || input.ServiceID == "" || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.CreatedAtMillis <= 0 {
		return errors.New("detach service domain input is incomplete")
	}
	hostname, err := publichostname.Normalize(input.Hostname)
	if err != nil {
		return err
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		if _, err := loadDomainTarget(ctx, transaction, input.ProjectID, input.ServiceID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
DELETE FROM service_domains WHERE hostname = ? AND service_id = ?`, hostname, input.ServiceID)
		if err != nil {
			return fmt.Errorf("detach service domain: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count detached service domain: %w", err)
		}
		if changed != 1 {
			return ErrDomainNotFound
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.domain.detach", ServiceID: input.ServiceID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
			Metadata: map[string]string{"hostname": hostname},
		})
	})
}

func loadDomainTarget(ctx context.Context, transaction *sql.Tx, projectID, serviceID string) (ServiceDomain, error) {
	var target ServiceDomain
	var targetPort sql.NullInt64
	err := transaction.QueryRowContext(ctx, `
SELECT s.id, s.name, s.project_id, p.name, s.target_port
FROM services s JOIN projects p ON p.id = s.project_id
WHERE s.id = ? AND s.project_id = ?`, serviceID, projectID).Scan(
		&target.ServiceID, &target.ServiceName, &target.ProjectID, &target.ProjectName, &targetPort,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDomain{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceDomain{}, fmt.Errorf("load domain target service: %w", err)
	}
	if !targetPort.Valid {
		return ServiceDomain{}, ErrServiceTargetPortNeeded
	}
	return target, nil
}

func loadServiceDomain(ctx context.Context, transaction *sql.Tx, hostname string) (ServiceDomain, bool, error) {
	var domain ServiceDomain
	err := transaction.QueryRowContext(ctx, `
SELECT d.hostname, d.service_id, s.name, s.project_id, p.name, d.created_at
FROM service_domains d
JOIN services s ON s.id = d.service_id
JOIN projects p ON p.id = s.project_id
WHERE d.hostname = ?`, hostname).Scan(
		&domain.Hostname, &domain.ServiceID, &domain.ServiceName,
		&domain.ProjectID, &domain.ProjectName, &domain.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceDomain{}, false, nil
	}
	if err != nil {
		return ServiceDomain{}, false, fmt.Errorf("load existing service domain: %w", err)
	}
	return domain, true, nil
}

func publicHostnameRoleExists(ctx context.Context, transaction *sql.Tx, hostname string) (bool, error) {
	var exists int
	err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM installation WHERE admin_hostname = ? OR automation_hostname = ?
  UNION ALL SELECT 1 FROM object_stores WHERE public_hostname = ?
)`, hostname, hostname, hostname).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check public hostname roles: %w", err)
	}
	return exists == 1, nil
}

func scanServiceDomains(rows *sql.Rows) ([]ServiceDomain, error) {
	domains := make([]ServiceDomain, 0)
	for rows.Next() {
		var domain ServiceDomain
		if err := rows.Scan(
			&domain.Hostname, &domain.ServiceID, &domain.ServiceName,
			&domain.ProjectID, &domain.ProjectName, &domain.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan service domain: %w", err)
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service domains: %w", err)
	}
	return domains, nil
}
