package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrServiceListenerNotFound = errors.New("service listener not found")
	ErrPublicPortReserved      = errors.New("public TCP port 443 is reserved by platformd HTTPS ingress")
	ErrPublicPortUnavailable   = errors.New("public port is already in use on this server")
)

type ServiceListener struct {
	Protocol    string
	PublicPort  int
	TargetPort  int
	ServiceID   string
	ServiceName string
	ProjectID   string
	ProjectName string
	CreatedAt   int64
}

type ListenerConflict struct {
	Listener ServiceListener
}

func (conflict *ListenerConflict) Error() string {
	return fmt.Sprintf(
		"public %s port %d belongs to service %s in project %s",
		conflict.Listener.Protocol,
		conflict.Listener.PublicPort,
		conflict.Listener.ServiceName,
		conflict.Listener.ProjectName,
	)
}

type AttachServiceListenerInput struct {
	ProjectID            string
	ServiceID            string
	Protocol             string
	PublicPort           int
	TargetPort           int
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type DetachServiceListenerInput struct {
	ProjectID            string
	ServiceID            string
	Protocol             string
	PublicPort           int
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

func (store *Store) ServiceListeners(ctx context.Context, projectID, serviceID string) ([]ServiceListener, error) {
	if _, err := store.Service(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT l.protocol, l.public_port, l.target_port, l.service_id,
       s.name, s.project_id, p.name, l.created_at
FROM service_listeners l
JOIN services s ON s.id = l.service_id
JOIN projects p ON p.id = s.project_id
WHERE l.service_id = ?
ORDER BY l.protocol, l.public_port`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service listeners: %w", err)
	}
	defer rows.Close()
	return scanServiceListeners(rows)
}

func (store *Store) ApplicationListeners(ctx context.Context) ([]ServiceListener, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT l.protocol, l.public_port, l.target_port, l.service_id,
       s.name, s.project_id, p.name, l.created_at
FROM service_listeners l
JOIN services s ON s.id = l.service_id
JOIN projects p ON p.id = s.project_id
ORDER BY l.protocol, l.public_port`)
	if err != nil {
		return nil, fmt.Errorf("list application listeners: %w", err)
	}
	defer rows.Close()
	return scanServiceListeners(rows)
}

func (store *Store) AttachServiceListener(ctx context.Context, input AttachServiceListenerInput) (ServiceListener, error) {
	protocol, err := normalizeListenerProtocol(input.Protocol)
	if err != nil {
		return ServiceListener{}, err
	}
	if input.ProjectID == "" || input.ServiceID == "" || !validListenerPort(input.PublicPort) || !validListenerPort(input.TargetPort) || input.AuditEventID == "" || input.CreatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return ServiceListener{}, errors.New("attach service listener input is incomplete")
	}
	if protocol == "tcp" && input.PublicPort == 443 {
		return ServiceListener{}, ErrPublicPortReserved
	}
	var listener ServiceListener
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		target, err := loadListenerTarget(ctx, transaction, input.ProjectID, input.ServiceID)
		if err != nil {
			return err
		}
		existing, exists, err := loadServiceListener(ctx, transaction, protocol, input.PublicPort)
		if err != nil {
			return err
		}
		action := "service.listener.attach"
		createdAt := input.CreatedAtMillis
		switch {
		case exists && existing.ServiceID != input.ServiceID:
			return &ListenerConflict{Listener: existing}
		case exists:
			createdAt = existing.CreatedAt
			action = "service.listener.update"
			if _, err := transaction.ExecContext(ctx, `
UPDATE service_listeners SET target_port = ?
WHERE protocol = ? AND public_port = ?`, input.TargetPort, protocol, input.PublicPort); err != nil {
				return fmt.Errorf("update service listener: %w", err)
			}
		default:
			if _, err := transaction.ExecContext(ctx, `
INSERT INTO service_listeners(protocol, public_port, service_id, target_port, created_at)
VALUES (?, ?, ?, ?, ?)`, protocol, input.PublicPort, input.ServiceID, input.TargetPort, input.CreatedAtMillis); err != nil {
				return fmt.Errorf("attach service listener: %w", err)
			}
		}
		listener = target
		listener.Protocol = protocol
		listener.PublicPort = input.PublicPort
		listener.TargetPort = input.TargetPort
		listener.CreatedAt = createdAt
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: action, ServiceID: input.ServiceID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
			Metadata: map[string]string{
				"protocol": protocol, "publicPort": fmt.Sprintf("%d", input.PublicPort),
				"targetPort": fmt.Sprintf("%d", input.TargetPort),
			},
		})
	})
	if err != nil {
		return ServiceListener{}, err
	}
	return listener, nil
}

func (store *Store) DetachServiceListener(ctx context.Context, input DetachServiceListenerInput) error {
	protocol, err := normalizeListenerProtocol(input.Protocol)
	if err != nil {
		return err
	}
	if input.ProjectID == "" || input.ServiceID == "" || !validListenerPort(input.PublicPort) || input.AuditEventID == "" || input.CreatedAtMillis <= 0 || validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail) != nil {
		return errors.New("detach service listener input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if _, err := loadListenerTarget(ctx, transaction, input.ProjectID, input.ServiceID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
DELETE FROM service_listeners
WHERE protocol = ? AND public_port = ? AND service_id = ?`, protocol, input.PublicPort, input.ServiceID)
		if err != nil {
			return fmt.Errorf("detach service listener: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count detached service listener: %w", err)
		}
		if changed != 1 {
			return ErrServiceListenerNotFound
		}
		return insertServiceAudit(ctx, transaction, serviceAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
			Action: "service.listener.detach", ServiceID: input.ServiceID,
			CorrelationID: input.RequestCorrelationID, CreatedAtMillis: input.CreatedAtMillis,
			Metadata: map[string]string{"protocol": protocol, "publicPort": fmt.Sprintf("%d", input.PublicPort)},
		})
	})
}

func normalizeListenerProtocol(protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return "tcp", nil
	case "udp":
		return "udp", nil
	default:
		return "", errors.New("listener protocol must be tcp or udp")
	}
}

func validListenerPort(port int) bool {
	return port >= 1 && port <= 65535
}

func loadListenerTarget(ctx context.Context, transaction *sql.Tx, projectID, serviceID string) (ServiceListener, error) {
	var target ServiceListener
	err := transaction.QueryRowContext(ctx, `
SELECT s.id, s.name, s.project_id, p.name
FROM services s JOIN projects p ON p.id = s.project_id
WHERE s.id = ? AND s.project_id = ?`, serviceID, projectID).Scan(
		&target.ServiceID, &target.ServiceName, &target.ProjectID, &target.ProjectName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceListener{}, ErrServiceNotFound
	}
	if err != nil {
		return ServiceListener{}, fmt.Errorf("load listener target service: %w", err)
	}
	return target, nil
}

func loadServiceListener(ctx context.Context, transaction *sql.Tx, protocol string, publicPort int) (ServiceListener, bool, error) {
	var listener ServiceListener
	err := transaction.QueryRowContext(ctx, `
SELECT l.protocol, l.public_port, l.target_port, l.service_id,
       s.name, s.project_id, p.name, l.created_at
FROM service_listeners l
JOIN services s ON s.id = l.service_id
JOIN projects p ON p.id = s.project_id
WHERE l.protocol = ? AND l.public_port = ?`, protocol, publicPort).Scan(
		&listener.Protocol, &listener.PublicPort, &listener.TargetPort, &listener.ServiceID,
		&listener.ServiceName, &listener.ProjectID, &listener.ProjectName, &listener.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceListener{}, false, nil
	}
	if err != nil {
		return ServiceListener{}, false, fmt.Errorf("load service listener: %w", err)
	}
	return listener, true, nil
}

func scanServiceListeners(rows *sql.Rows) ([]ServiceListener, error) {
	listeners := make([]ServiceListener, 0)
	for rows.Next() {
		var listener ServiceListener
		if err := rows.Scan(
			&listener.Protocol, &listener.PublicPort, &listener.TargetPort, &listener.ServiceID,
			&listener.ServiceName, &listener.ProjectID, &listener.ProjectName, &listener.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan service listener: %w", err)
		}
		listeners = append(listeners, listener)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service listeners: %w", err)
	}
	return listeners, nil
}
