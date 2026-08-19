package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/iivankin/platformd/internal/resourcename"
)

var (
	ErrHostNotFound          = errors.New("host not found")
	ErrHostNameConflict      = errors.New("host name already exists")
	ErrHostHasServices       = errors.New("host still has assigned services")
	ErrJoinTokenNotFound     = errors.New("host join token not found")
	ErrJoinTokenExpired      = errors.New("host join token expired")
	ErrJoinTokenConsumed     = errors.New("host join token already used")
	ErrInvalidPublicIPv4     = errors.New("public IPv4 address is invalid")
	ErrServiceHostHasVolumes = errors.New("services with volumes cannot change hosts")
	ErrUnknownServiceHost    = errors.New("service host does not exist")
)

type Host struct {
	ID              string
	Name            string
	PublicIPv4      string
	TokenHMAC       []byte
	LastSeenMillis  *int64
	JoinedAtMillis  int64
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type HostJoinToken struct {
	ID               string
	Name             string
	TokenHMAC        []byte
	ExpiresAtMillis  int64
	ConsumedAtMillis *int64
	CreatedAtMillis  int64
}

type CreateHostJoinTokenInput struct {
	ID                   string
	Name                 string
	TokenHMAC            []byte
	ExpiresAtMillis      int64
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type ConsumeHostJoinTokenInput struct {
	TokenID          string
	HostID           string
	AuditEventID     string
	Name             string
	PublicIPv4       string
	TokenHMAC        []byte
	ConsumedAtMillis int64
}

type UpdateHostRuntimeInput struct {
	ID              string
	PublicIPv4      string
	LastSeenMillis  int64
	UpdatedAtMillis int64
}

type DeleteHostInput struct {
	ID                   string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

type DeleteHostJoinTokenInput struct {
	ID                   string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func ValidatePublicIPv4(value string) error {
	if value == "" {
		return ErrInvalidPublicIPv4
	}
	parsed, err := netip.ParseAddr(value)
	if err != nil || !parsed.Is4() || parsed.IsUnspecified() || parsed.IsLoopback() || parsed.IsMulticast() || parsed.IsLinkLocalUnicast() {
		return ErrInvalidPublicIPv4
	}
	if ip := net.ParseIP(value); ip == nil || ip.To4() == nil {
		return ErrInvalidPublicIPv4
	}
	return nil
}

func (store *Store) Hosts(ctx context.Context) ([]Host, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, public_ipv4, last_seen_at, joined_at, created_at, updated_at
FROM hosts ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer rows.Close()
	hosts := make([]Host, 0)
	for rows.Next() {
		host, err := scanHost(rows, false)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hosts: %w", err)
	}
	return hosts, nil
}

func (store *Store) Host(ctx context.Context, hostID string) (Host, error) {
	host, err := scanHost(store.database.QueryRowContext(ctx, `
SELECT id, name, public_ipv4, last_seen_at, joined_at, created_at, updated_at
FROM hosts WHERE id = ?`, hostID), false)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrHostNotFound
	}
	return host, err
}

func (store *Store) HostCredential(ctx context.Context, hostID string) (Host, error) {
	host, err := scanHost(store.database.QueryRowContext(ctx, `
SELECT id, name, public_ipv4, token_hmac, last_seen_at, joined_at, created_at, updated_at
FROM hosts WHERE id = ?`, hostID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrHostNotFound
	}
	return host, err
}

func (store *Store) ActiveHostJoinTokens(ctx context.Context) ([]HostJoinToken, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, expires_at, consumed_at, created_at
FROM host_join_tokens WHERE consumed_at IS NULL ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list host join tokens: %w", err)
	}
	defer rows.Close()
	tokens := make([]HostJoinToken, 0)
	for rows.Next() {
		token, err := scanJoinToken(rows, false)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate host join tokens: %w", err)
	}
	return tokens, nil
}

func (store *Store) CreateHostJoinToken(ctx context.Context, input CreateHostJoinTokenInput) (HostJoinToken, error) {
	if input.ID == "" || input.AuditEventID == "" || input.ActorID == "" ||
		input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis || len(input.TokenHMAC) != sha256.Size {
		return HostJoinToken{}, errors.New("create host join token input is incomplete")
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return HostJoinToken{}, err
	}
	metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail, "name": input.Name})
	if err != nil {
		return HostJoinToken{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO host_join_tokens(id, name, token_hmac, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)`, input.ID, input.Name, input.TokenHMAC, input.ExpiresAtMillis, input.CreatedAtMillis); err != nil {
			return fmt.Errorf("create host join token: %w", err)
		}
		return insertHostAudit(ctx, transaction, input.AuditEventID, input.ActorKind, input.ActorID, "host_join_token.create", "host_join_token", input.ID, input.RequestCorrelationID, metadata, input.CreatedAtMillis)
	})
	if err != nil {
		return HostJoinToken{}, err
	}
	return HostJoinToken{
		ID: input.ID, Name: input.Name, ExpiresAtMillis: input.ExpiresAtMillis, CreatedAtMillis: input.CreatedAtMillis,
	}, nil
}

func (store *Store) HostJoinTokenCredential(ctx context.Context, tokenID string) (HostJoinToken, error) {
	token, err := scanJoinToken(store.database.QueryRowContext(ctx, `
SELECT id, name, token_hmac, expires_at, consumed_at, created_at
FROM host_join_tokens WHERE id = ?`, tokenID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return HostJoinToken{}, ErrJoinTokenNotFound
	}
	return token, err
}

func (store *Store) ConsumeHostJoinToken(ctx context.Context, input ConsumeHostJoinTokenInput) (Host, error) {
	if input.TokenID == "" || input.HostID == "" || input.AuditEventID == "" || input.ConsumedAtMillis <= 0 || len(input.TokenHMAC) != sha256.Size {
		return Host{}, errors.New("consume host join token input is incomplete")
	}
	if input.Name != "" {
		if err := resourcename.Validate(input.Name); err != nil {
			return Host{}, err
		}
	}
	if err := ValidatePublicIPv4(input.PublicIPv4); err != nil {
		return Host{}, err
	}
	err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var name string
		var expiresAt int64
		var consumedAt sql.NullInt64
		if err := transaction.QueryRowContext(ctx, `
SELECT name, expires_at, consumed_at FROM host_join_tokens WHERE id = ?`, input.TokenID).Scan(&name, &expiresAt, &consumedAt); errors.Is(err, sql.ErrNoRows) {
			return ErrJoinTokenNotFound
		} else if err != nil {
			return fmt.Errorf("load host join token: %w", err)
		}
		if consumedAt.Valid {
			return ErrJoinTokenConsumed
		}
		if input.ConsumedAtMillis > expiresAt {
			return ErrJoinTokenExpired
		}
		if input.Name == "" {
			input.Name = name
		}
		if err := resourcename.Validate(input.Name); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE host_join_tokens SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, input.ConsumedAtMillis, input.TokenID)
		if err != nil {
			return fmt.Errorf("consume host join token: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count consumed host join token: %w", err)
		}
		if changed != 1 {
			return ErrJoinTokenConsumed
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO hosts(id, name, public_ipv4, token_hmac, last_seen_at, joined_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			input.HostID, input.Name, input.PublicIPv4, input.TokenHMAC,
			input.ConsumedAtMillis, input.ConsumedAtMillis, input.ConsumedAtMillis, input.ConsumedAtMillis,
		); err != nil {
			if isUniqueConstraint(err) {
				return ErrHostNameConflict
			}
			return fmt.Errorf("create host: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{"name": input.Name, "publicIpv4": input.PublicIPv4})
		if err != nil {
			return err
		}
		return insertHostAudit(ctx, transaction, input.AuditEventID, "system", "system", "host.join", "host", input.HostID, "", metadata, input.ConsumedAtMillis)
	})
	if err != nil {
		return Host{}, err
	}
	return store.Host(ctx, input.HostID)
}

func (store *Store) UpdateHostRuntime(ctx context.Context, input UpdateHostRuntimeInput) error {
	if input.ID == "" || input.LastSeenMillis <= 0 || input.UpdatedAtMillis <= 0 {
		return errors.New("update host runtime input is incomplete")
	}
	if input.PublicIPv4 != "" {
		if err := ValidatePublicIPv4(input.PublicIPv4); err != nil {
			return err
		}
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE hosts SET public_ipv4 = COALESCE(?, public_ipv4), last_seen_at = ?, updated_at = ?
WHERE id = ?`, nullableString(input.PublicIPv4), input.LastSeenMillis, input.UpdatedAtMillis, input.ID)
		if err != nil {
			return fmt.Errorf("update host runtime: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count host runtime update: %w", err)
		}
		if changed != 1 {
			return ErrHostNotFound
		}
		return nil
	})
}

func (store *Store) DeleteHost(ctx context.Context, input DeleteHostInput) error {
	if input.ID == "" || input.AuditEventID == "" || input.ActorID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete host input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var name string
		if err := transaction.QueryRowContext(ctx, "SELECT name FROM hosts WHERE id = ?", input.ID).Scan(&name); errors.Is(err, sql.ErrNoRows) {
			return ErrHostNotFound
		} else if err != nil {
			return fmt.Errorf("load host before delete: %w", err)
		}
		var assigned int
		if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM services WHERE host_id = ?", input.ID).Scan(&assigned); err != nil {
			return fmt.Errorf("count host services: %w", err)
		}
		if assigned > 0 {
			return ErrHostHasServices
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM hosts WHERE id = ?", input.ID); err != nil {
			return fmt.Errorf("delete host: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail, "name": name})
		if err != nil {
			return err
		}
		return insertHostAudit(ctx, transaction, input.AuditEventID, input.ActorKind, input.ActorID, "host.delete", "host", input.ID, input.RequestCorrelationID, metadata, input.DeletedAtMillis)
	})
}

func (store *Store) DeleteHostJoinToken(ctx context.Context, input DeleteHostJoinTokenInput) error {
	if input.ID == "" || input.AuditEventID == "" || input.ActorID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete host join token input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var name string
		var consumed sql.NullInt64
		if err := transaction.QueryRowContext(ctx, "SELECT name, consumed_at FROM host_join_tokens WHERE id = ?", input.ID).Scan(&name, &consumed); errors.Is(err, sql.ErrNoRows) {
			return ErrJoinTokenNotFound
		} else if err != nil {
			return fmt.Errorf("load host join token before delete: %w", err)
		}
		if consumed.Valid {
			return ErrJoinTokenConsumed
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM host_join_tokens WHERE id = ? AND consumed_at IS NULL", input.ID); err != nil {
			return fmt.Errorf("delete host join token: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{"actorEmail": input.ActorEmail, "name": name})
		if err != nil {
			return err
		}
		return insertHostAudit(ctx, transaction, input.AuditEventID, input.ActorKind, input.ActorID, "host_join_token.delete", "host_join_token", input.ID, input.RequestCorrelationID, metadata, input.DeletedAtMillis)
	})
}

func (store *Store) EnabledServiceIDsOnHost(ctx context.Context, hostID string) ([]string, error) {
	query := "SELECT id FROM services WHERE enabled = 1 AND host_id IS NULL ORDER BY id"
	args := []any{}
	if hostID != "" {
		query = "SELECT id FROM services WHERE enabled = 1 AND host_id = ? ORDER BY id"
		args = []any{hostID}
	}
	rows, err := store.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list enabled services on host: %w", err)
	}
	defer rows.Close()
	serviceIDs := make([]string, 0)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, fmt.Errorf("scan enabled host service: %w", err)
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled host services: %w", err)
	}
	return serviceIDs, nil
}

func scanHost(scanner interface{ Scan(...any) error }, includeSecret bool) (Host, error) {
	var host Host
	var publicIPv4 sql.NullString
	var lastSeen sql.NullInt64
	destinations := []any{&host.ID, &host.Name, &publicIPv4}
	if includeSecret {
		destinations = append(destinations, &host.TokenHMAC)
	}
	destinations = append(destinations, &lastSeen, &host.JoinedAtMillis, &host.CreatedAtMillis, &host.UpdatedAtMillis)
	if err := scanner.Scan(destinations...); err != nil {
		return Host{}, fmt.Errorf("scan host: %w", err)
	}
	host.PublicIPv4 = publicIPv4.String
	if lastSeen.Valid {
		host.LastSeenMillis = &lastSeen.Int64
	}
	return host, nil
}

func scanJoinToken(scanner interface{ Scan(...any) error }, includeSecret bool) (HostJoinToken, error) {
	var token HostJoinToken
	var consumed sql.NullInt64
	destinations := []any{&token.ID, &token.Name}
	if includeSecret {
		destinations = append(destinations, &token.TokenHMAC)
	}
	destinations = append(destinations, &token.ExpiresAtMillis, &consumed, &token.CreatedAtMillis)
	if err := scanner.Scan(destinations...); err != nil {
		return HostJoinToken{}, fmt.Errorf("scan host join token: %w", err)
	}
	if consumed.Valid {
		token.ConsumedAtMillis = &consumed.Int64
	}
	return token, nil
}

func insertHostAudit(ctx context.Context, transaction *sql.Tx, id, actorKind, actorID, action, targetKind, targetID, correlationID string, metadata []byte, timestamp int64) error {
	var requestID any
	if correlationID != "" {
		requestID = correlationID
	}
	if actorKind == "" {
		actorKind = "access"
	}
	if actorID == "system" {
		actorKind = "system"
	}
	_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'succeeded', ?, ?)`,
		id, actorKind, actorID, action, targetKind, targetID, requestID, string(metadata), timestamp,
	)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
