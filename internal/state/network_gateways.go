package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/iivankin/platformd/internal/resourcename"
)

const (
	firstGatewaySlot = 192
	lastGatewaySlot  = 254
)

var (
	ErrNetworkGatewayNotFound      = errors.New("network gateway not found")
	ErrNetworkGatewaySlotExhausted = errors.New("project network gateway address space is exhausted")
	remoteHostPattern              = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)
)

type NetworkGateway struct {
	ID              string
	ProjectID       string
	ProjectName     string
	Name            string
	Mode            string
	Transport       string
	Protocol        string
	InterfaceName   string
	SourceAddress   string
	ListenPort      int
	InternalSlot    int
	RemoteHost      string
	RemotePort      int
	TargetServiceID string
	TargetService   string
	TargetPort      int
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type NetworkGatewayConfiguration struct {
	Name            string
	Mode            string
	Transport       string
	Protocol        string
	InterfaceName   string
	SourceAddress   string
	ListenPort      int
	RemoteHost      string
	RemotePort      int
	TargetServiceID string
	TargetPort      int
}

type CreateNetworkGateway struct {
	ID                   string
	ProjectID            string
	Configuration        NetworkGatewayConfiguration
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type UpdateNetworkGateway struct {
	ID                   string
	ProjectID            string
	Configuration        NetworkGatewayConfiguration
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

type DeleteNetworkGateway struct {
	ID                   string
	ProjectID            string
	AuditEventID         string
	ActorKind            string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	DeletedAtMillis      int64
}

func (store *Store) NetworkGateways(ctx context.Context, projectID string) ([]NetworkGateway, error) {
	if projectID == "" {
		return nil, ErrProjectNotFound
	}
	rows, err := store.database.QueryContext(ctx, networkGatewaySelect+`
WHERE g.project_id = ? ORDER BY g.name, g.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list network gateways: %w", err)
	}
	defer rows.Close()
	return scanNetworkGateways(rows)
}

func (store *Store) ApplicationNetworkGateways(ctx context.Context) ([]NetworkGateway, error) {
	rows, err := store.database.QueryContext(ctx, networkGatewaySelect+`
ORDER BY g.project_id, g.name, g.id`)
	if err != nil {
		return nil, fmt.Errorf("list application network gateways: %w", err)
	}
	defer rows.Close()
	return scanNetworkGateways(rows)
}

func (store *Store) NetworkGateway(ctx context.Context, projectID, gatewayID string) (NetworkGateway, error) {
	row := store.database.QueryRowContext(ctx, networkGatewaySelect+`
WHERE g.project_id = ? AND g.id = ?`, projectID, gatewayID)
	return scanNetworkGateway(row)
}

func (store *Store) CreateNetworkGateway(ctx context.Context, input CreateNetworkGateway) (NetworkGateway, error) {
	if input.ID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.CreatedAtMillis <= 0 {
		return NetworkGateway{}, errors.New("create network gateway input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return NetworkGateway{}, err
	}
	configuration, err := normalizeNetworkGatewayConfiguration(input.Configuration)
	if err != nil {
		return NetworkGateway{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireNetworkGatewayProject(ctx, transaction, input.ProjectID); err != nil {
			return err
		}
		exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, configuration.Name)
		if err != nil {
			return err
		}
		if exists {
			return ErrResourceNameConflict
		}
		if err := validateNetworkGatewayTarget(ctx, transaction, input.ProjectID, configuration); err != nil {
			return err
		}
		internalSlot := 0
		if configuration.Mode == "import" {
			internalSlot, err = allocateNetworkGatewaySlot(ctx, transaction, input.ProjectID, "")
			if err != nil {
				return err
			}
		}
		if err := insertNetworkGateway(ctx, transaction, input.ID, input.ProjectID, configuration, internalSlot, input.CreatedAtMillis); err != nil {
			return err
		}
		return insertNetworkGatewayAudit(ctx, transaction, networkGatewayAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID,
			ActorEmail: input.ActorEmail, Action: "network_gateway.create", GatewayID: input.ID,
			CorrelationID: input.RequestCorrelationID, Timestamp: input.CreatedAtMillis,
			Configuration: configuration,
		})
	})
	if err != nil {
		return NetworkGateway{}, err
	}
	return store.NetworkGateway(ctx, input.ProjectID, input.ID)
}

func (store *Store) UpdateNetworkGateway(ctx context.Context, input UpdateNetworkGateway) (NetworkGateway, error) {
	if input.ID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.UpdatedAtMillis <= 0 {
		return NetworkGateway{}, errors.New("update network gateway input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return NetworkGateway{}, err
	}
	configuration, err := normalizeNetworkGatewayConfiguration(input.Configuration)
	if err != nil {
		return NetworkGateway{}, err
	}
	err = store.WriteControl(ctx, func(transaction *sql.Tx) error {
		current, err := loadNetworkGateway(ctx, transaction, input.ProjectID, input.ID)
		if err != nil {
			return err
		}
		if current.Name != configuration.Name {
			exists, err := projectResourceNameExists(ctx, transaction, input.ProjectID, configuration.Name)
			if err != nil {
				return err
			}
			if exists {
				return ErrResourceNameConflict
			}
		}
		if err := validateNetworkGatewayTarget(ctx, transaction, input.ProjectID, configuration); err != nil {
			return err
		}
		internalSlot := 0
		if configuration.Mode == "import" {
			if current.Mode == "import" {
				internalSlot = current.InternalSlot
			} else {
				internalSlot, err = allocateNetworkGatewaySlot(ctx, transaction, input.ProjectID, input.ID)
				if err != nil {
					return err
				}
			}
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE network_gateways SET
  name = ?, mode = ?, transport = ?, protocol = ?, interface_name = ?,
  source_address = ?, listen_port = ?, internal_slot = ?, remote_host = ?,
  remote_port = ?, target_service_id = ?, target_port = ?, updated_at = ?
WHERE id = ? AND project_id = ?`, configuration.Name, configuration.Mode, configuration.Transport,
			configuration.Protocol, nullableString(configuration.InterfaceName), nullableString(configuration.SourceAddress),
			configuration.ListenPort, nullablePositive(int64(internalSlot)), nullableString(configuration.RemoteHost),
			nullablePositive(int64(configuration.RemotePort)), nullableString(configuration.TargetServiceID),
			nullablePositive(int64(configuration.TargetPort)), input.UpdatedAtMillis, input.ID, input.ProjectID); err != nil {
			return fmt.Errorf("update network gateway: %w", err)
		}
		return insertNetworkGatewayAudit(ctx, transaction, networkGatewayAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID,
			ActorEmail: input.ActorEmail, Action: "network_gateway.update", GatewayID: input.ID,
			CorrelationID: input.RequestCorrelationID, Timestamp: input.UpdatedAtMillis,
			Configuration: configuration,
		})
	})
	if err != nil {
		return NetworkGateway{}, err
	}
	return store.NetworkGateway(ctx, input.ProjectID, input.ID)
}

func (store *Store) DeleteNetworkGateway(ctx context.Context, input DeleteNetworkGateway) error {
	if input.ID == "" || input.ProjectID == "" || input.AuditEventID == "" || input.DeletedAtMillis <= 0 {
		return errors.New("delete network gateway input is incomplete")
	}
	if err := validateMutationActor(input.ActorKind, input.ActorID, input.ActorEmail); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		current, err := loadNetworkGateway(ctx, transaction, input.ProjectID, input.ID)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM network_gateways WHERE id = ? AND project_id = ?`, input.ID, input.ProjectID); err != nil {
			return fmt.Errorf("delete network gateway: %w", err)
		}
		return insertNetworkGatewayAudit(ctx, transaction, networkGatewayAudit{
			ID: input.AuditEventID, ActorKind: input.ActorKind, ActorID: input.ActorID,
			ActorEmail: input.ActorEmail, Action: "network_gateway.delete", GatewayID: input.ID,
			CorrelationID: input.RequestCorrelationID, Timestamp: input.DeletedAtMillis,
			Configuration: configurationFromNetworkGateway(current),
		})
	})
}

func normalizeNetworkGatewayConfiguration(configuration NetworkGatewayConfiguration) (NetworkGatewayConfiguration, error) {
	configuration.Name = strings.TrimSpace(configuration.Name)
	configuration.Mode = strings.ToLower(strings.TrimSpace(configuration.Mode))
	configuration.Transport = strings.ToLower(strings.TrimSpace(configuration.Transport))
	configuration.Protocol = strings.ToLower(strings.TrimSpace(configuration.Protocol))
	configuration.InterfaceName = strings.TrimSpace(configuration.InterfaceName)
	configuration.SourceAddress = strings.TrimSpace(configuration.SourceAddress)
	configuration.RemoteHost = strings.TrimSpace(configuration.RemoteHost)
	if err := resourcename.Validate(configuration.Name); err != nil {
		return NetworkGatewayConfiguration{}, err
	}
	if configuration.Mode != "import" && configuration.Mode != "export" {
		return NetworkGatewayConfiguration{}, errors.New("network gateway mode must be import or export")
	}
	if configuration.Transport != "vpc" && configuration.Transport != "mesh" {
		return NetworkGatewayConfiguration{}, errors.New("network gateway transport must be vpc or mesh")
	}
	if configuration.Protocol != "tcp" && configuration.Protocol != "udp" {
		return NetworkGatewayConfiguration{}, errors.New("network gateway protocol must be tcp or udp")
	}
	if configuration.Transport == "mesh" {
		configuration.InterfaceName = ""
		configuration.SourceAddress = ""
	} else {
		if configuration.InterfaceName == "" || len(configuration.InterfaceName) > 15 || strings.ContainsAny(configuration.InterfaceName, "\x00\r\n/ ") {
			return NetworkGatewayConfiguration{}, errors.New("network gateway interface name is invalid")
		}
		address, err := netip.ParseAddr(configuration.SourceAddress)
		if err != nil || !address.Is4() || address.IsUnspecified() || address.IsLoopback() {
			return NetworkGatewayConfiguration{}, errors.New("network gateway source address must be a non-loopback IPv4 address")
		}
		configuration.SourceAddress = address.String()
	}
	if !validNetworkGatewayPort(configuration.ListenPort) {
		return NetworkGatewayConfiguration{}, errors.New("network gateway listen port must be between 1 and 65535")
	}
	if configuration.Mode == "import" {
		if !validRemoteHost(configuration.RemoteHost) || !validNetworkGatewayPort(configuration.RemotePort) {
			return NetworkGatewayConfiguration{}, errors.New("import gateway requires a remote host and port")
		}
		configuration.TargetServiceID = ""
		configuration.TargetPort = 0
	} else {
		if configuration.TargetServiceID == "" || !validNetworkGatewayPort(configuration.TargetPort) {
			return NetworkGatewayConfiguration{}, errors.New("export gateway requires a target service and port")
		}
		configuration.RemoteHost = ""
		configuration.RemotePort = 0
	}
	return configuration, nil
}

func validRemoteHost(value string) bool {
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Is4() && !address.IsUnspecified()
	}
	return len(value) <= 253 && remoteHostPattern.MatchString(value)
}

func validNetworkGatewayPort(port int) bool {
	return port >= 1 && port <= 65_535
}

func requireNetworkGatewayProject(ctx context.Context, transaction *sql.Tx, projectID string) error {
	var exists int
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)`, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("load network gateway project: %w", err)
	}
	if exists == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func validateNetworkGatewayTarget(ctx context.Context, transaction *sql.Tx, projectID string, configuration NetworkGatewayConfiguration) error {
	if configuration.Mode != "export" {
		return nil
	}
	var exists int
	if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM services WHERE id = ? AND project_id = ?)`, configuration.TargetServiceID, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("load network gateway target service: %w", err)
	}
	if exists == 0 {
		return ErrServiceNotFound
	}
	return nil
}

func allocateNetworkGatewaySlot(ctx context.Context, transaction *sql.Tx, projectID, excludedID string) (int, error) {
	rows, err := transaction.QueryContext(ctx, `
SELECT internal_slot FROM network_gateways
WHERE project_id = ? AND mode = 'import' AND id != ? ORDER BY internal_slot`, projectID, excludedID)
	if err != nil {
		return 0, fmt.Errorf("list network gateway slots: %w", err)
	}
	defer rows.Close()
	used := make(map[int]struct{})
	for rows.Next() {
		var slot int
		if err := rows.Scan(&slot); err != nil {
			return 0, fmt.Errorf("scan network gateway slot: %w", err)
		}
		used[slot] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate network gateway slots: %w", err)
	}
	for slot := firstGatewaySlot; slot <= lastGatewaySlot; slot++ {
		if _, exists := used[slot]; !exists {
			return slot, nil
		}
	}
	return 0, ErrNetworkGatewaySlotExhausted
}

func insertNetworkGateway(ctx context.Context, transaction *sql.Tx, id, projectID string, configuration NetworkGatewayConfiguration, internalSlot int, timestamp int64) error {
	_, err := transaction.ExecContext(ctx, `
INSERT INTO network_gateways(
  id, project_id, name, mode, transport, protocol, interface_name,
  source_address, listen_port, internal_slot, remote_host, remote_port,
  target_service_id, target_port, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, projectID,
		configuration.Name, configuration.Mode, configuration.Transport, configuration.Protocol,
		nullableString(configuration.InterfaceName), nullableString(configuration.SourceAddress), configuration.ListenPort,
		nullablePositive(int64(internalSlot)), nullableString(configuration.RemoteHost), nullablePositive(int64(configuration.RemotePort)),
		nullableString(configuration.TargetServiceID), nullablePositive(int64(configuration.TargetPort)), timestamp, timestamp)
	if err != nil {
		return fmt.Errorf("create network gateway: %w", err)
	}
	return nil
}

type networkGatewayAudit struct {
	ID            string
	ActorKind     string
	ActorID       string
	ActorEmail    string
	Action        string
	GatewayID     string
	CorrelationID string
	Timestamp     int64
	Configuration NetworkGatewayConfiguration
}

func insertNetworkGatewayAudit(ctx context.Context, transaction *sql.Tx, audit networkGatewayAudit) error {
	metadata := map[string]any{
		"mode": audit.Configuration.Mode, "transport": audit.Configuration.Transport,
		"protocol": audit.Configuration.Protocol, "listenPort": audit.Configuration.ListenPort,
	}
	if audit.ActorEmail != "" {
		metadata["actorEmail"] = audit.ActorEmail
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, ?, ?, ?, 'network_gateway', ?, ?, 'succeeded', ?, ?)`, audit.ID, audit.ActorKind,
		audit.ActorID, audit.Action, audit.GatewayID, nullableString(audit.CorrelationID), string(encoded), audit.Timestamp); err != nil {
		return fmt.Errorf("audit network gateway mutation: %w", err)
	}
	return nil
}

const networkGatewaySelect = `
SELECT g.id, g.project_id, p.name, g.name, g.mode, g.transport, g.protocol,
       g.interface_name, g.source_address, g.listen_port, g.internal_slot,
       g.remote_host, g.remote_port, g.target_service_id, COALESCE(s.name, ''),
       g.target_port, g.created_at, g.updated_at
FROM network_gateways g
JOIN projects p ON p.id = g.project_id
LEFT JOIN services s ON s.id = g.target_service_id `

type networkGatewayScanner interface {
	Scan(...any) error
}

func scanNetworkGateway(scanner networkGatewayScanner) (NetworkGateway, error) {
	var gateway NetworkGateway
	var internalSlot, remotePort, targetPort sql.NullInt64
	var interfaceName, sourceAddress, remoteHost, targetServiceID sql.NullString
	err := scanner.Scan(
		&gateway.ID, &gateway.ProjectID, &gateway.ProjectName, &gateway.Name,
		&gateway.Mode, &gateway.Transport, &gateway.Protocol, &interfaceName,
		&sourceAddress, &gateway.ListenPort, &internalSlot, &remoteHost, &remotePort,
		&targetServiceID, &gateway.TargetService, &targetPort,
		&gateway.CreatedAtMillis, &gateway.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkGateway{}, ErrNetworkGatewayNotFound
	}
	if err != nil {
		return NetworkGateway{}, fmt.Errorf("scan network gateway: %w", err)
	}
	gateway.InterfaceName = interfaceName.String
	gateway.SourceAddress = sourceAddress.String
	gateway.InternalSlot = int(internalSlot.Int64)
	gateway.RemoteHost = remoteHost.String
	gateway.RemotePort = int(remotePort.Int64)
	gateway.TargetServiceID = targetServiceID.String
	gateway.TargetPort = int(targetPort.Int64)
	return gateway, nil
}

func scanNetworkGateways(rows *sql.Rows) ([]NetworkGateway, error) {
	gateways := make([]NetworkGateway, 0)
	for rows.Next() {
		gateway, err := scanNetworkGateway(rows)
		if err != nil {
			return nil, err
		}
		gateways = append(gateways, gateway)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate network gateways: %w", err)
	}
	return gateways, nil
}

func loadNetworkGateway(ctx context.Context, transaction *sql.Tx, projectID, gatewayID string) (NetworkGateway, error) {
	return scanNetworkGateway(transaction.QueryRowContext(ctx, networkGatewaySelect+`
WHERE g.project_id = ? AND g.id = ?`, projectID, gatewayID))
}

func configurationFromNetworkGateway(gateway NetworkGateway) NetworkGatewayConfiguration {
	return NetworkGatewayConfiguration{
		Name: gateway.Name, Mode: gateway.Mode, Transport: gateway.Transport,
		Protocol: gateway.Protocol, InterfaceName: gateway.InterfaceName,
		SourceAddress: gateway.SourceAddress, ListenPort: gateway.ListenPort,
		RemoteHost: gateway.RemoteHost, RemotePort: gateway.RemotePort,
		TargetServiceID: gateway.TargetServiceID, TargetPort: gateway.TargetPort,
	}
}
