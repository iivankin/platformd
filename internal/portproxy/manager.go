package portproxy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/deployment"
)

type BackendResolver interface {
	ServiceBackend(string, int) (deployment.Backend, bool, error)
}

type Backend struct {
	ID            string
	Address       string
	Port          int
	SourceAddress string
}

// Target keeps destination resolution independent from the listener. Service
// routes follow the active deployment while address routes remain pinned to a
// VPC or Mesh endpoint.
type Target interface {
	Resolve(BackendResolver) (Backend, bool, error)
	Validate() error
}

type ServiceTarget struct {
	ServiceID string
	Port      int
}

func (target ServiceTarget) Validate() error {
	if target.ServiceID == "" || !validPort(target.Port) {
		return errors.New("service proxy target is incomplete")
	}
	return nil
}

func (target ServiceTarget) Resolve(resolver BackendResolver) (Backend, bool, error) {
	resolved, available, err := resolver.ServiceBackend(target.ServiceID, target.Port)
	if err != nil || !available {
		return Backend{}, available, err
	}
	return Backend{
		ID: resolved.DeploymentID, Address: resolved.Address, Port: resolved.Port,
	}, true, nil
}

type AddressTarget struct {
	Host          string
	Port          int
	SourceAddress string
}

func (target AddressTarget) Validate() error {
	if strings.TrimSpace(target.Host) == "" || strings.ContainsAny(target.Host, "\x00\r\n") || !validPort(target.Port) {
		return errors.New("address proxy target is incomplete")
	}
	if target.SourceAddress != "" {
		address, err := netip.ParseAddr(target.SourceAddress)
		if err != nil || !address.Is4() || address.IsUnspecified() {
			return errors.New("proxy source address must be a specific IPv4 address")
		}
	}
	return nil
}

func (target AddressTarget) Resolve(BackendResolver) (Backend, bool, error) {
	return Backend{
		ID:            "address:" + net.JoinHostPort(target.Host, strconv.Itoa(target.Port)),
		Address:       target.Host,
		Port:          target.Port,
		SourceAddress: target.SourceAddress,
	}, true, nil
}

type Route struct {
	ID                 string
	Protocol           string
	ListenAddress      string
	ListenPort         int
	ListenNamespacePID int
	DialNamespacePID   int
	Target             Target
}

type Config struct {
	Backends BackendResolver
	OnError  func(string, error)
}

type endpoint interface {
	Close() error
	Update(Route)
}

type endpointEntry struct {
	key      string
	endpoint endpoint
}

type Manager struct {
	backends  BackendResolver
	onError   func(string, error)
	mu        sync.Mutex
	endpoints map[string]endpointEntry
	owners    map[string]string
}

func New(config Config) (*Manager, error) {
	if config.Backends == nil {
		return nil, errors.New("port proxy requires a backend resolver")
	}
	onError := config.OnError
	if onError == nil {
		onError = func(string, error) {}
	}
	return &Manager{
		backends: config.Backends, onError: onError,
		endpoints: make(map[string]endpointEntry), owners: make(map[string]string),
	}, nil
}

func (manager *Manager) Add(route Route) error {
	protocol, err := normalizeProtocol(route.Protocol)
	if err != nil {
		return err
	}
	address, err := normalizeListenAddress(route.ListenAddress)
	if err != nil {
		return err
	}
	if route.ID == "" || !validPort(route.ListenPort) || route.Target == nil {
		return errors.New("proxy route is incomplete")
	}
	if route.ListenNamespacePID < 0 || route.DialNamespacePID < 0 {
		return errors.New("proxy network namespace PID cannot be negative")
	}
	if err := route.Target.Validate(); err != nil {
		return err
	}
	route.Protocol = protocol
	route.ListenAddress = address
	if route.ListenNamespacePID == 0 && protocol == "tcp" && route.ListenPort == 443 {
		return errors.New("TCP port 443 is reserved by platformd HTTPS ingress")
	}
	key := routeKey(protocol, address, route.ListenPort, route.ListenNamespacePID)

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if owner := manager.owners[key]; owner != "" && owner != route.ID {
		return fmt.Errorf("%s %s:%d is already owned by another route", protocol, address, route.ListenPort)
	}
	if current, exists := manager.endpoints[route.ID]; exists {
		if current.key != key {
			return errors.New("remove a proxy route before changing its listener address")
		}
		current.endpoint.Update(route)
		return nil
	}
	created, err := manager.listen(route)
	if err != nil {
		return fmt.Errorf("listen on %s %s:%d: %w", protocol, address, route.ListenPort, err)
	}
	manager.endpoints[route.ID] = endpointEntry{key: key, endpoint: created}
	manager.owners[key] = route.ID
	return nil
}

func (manager *Manager) Remove(routeID string) error {
	if routeID == "" {
		return nil
	}
	manager.mu.Lock()
	current, exists := manager.endpoints[routeID]
	if exists {
		delete(manager.endpoints, routeID)
		delete(manager.owners, current.key)
	}
	manager.mu.Unlock()
	if !exists {
		return nil
	}
	return current.endpoint.Close()
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	current := make([]endpoint, 0, len(manager.endpoints))
	for _, item := range manager.endpoints {
		current = append(current, item.endpoint)
	}
	manager.endpoints = make(map[string]endpointEntry)
	manager.owners = make(map[string]string)
	manager.mu.Unlock()
	var failures []error
	for _, item := range current {
		failures = append(failures, item.Close())
	}
	return errors.Join(failures...)
}

func (manager *Manager) listen(route Route) (endpoint, error) {
	listenAddress := net.JoinHostPort(route.ListenAddress, strconv.Itoa(route.ListenPort))
	switch route.Protocol {
	case "tcp":
		listener, err := listenTCPInNamespace(route.ListenNamespacePID, listenAddress)
		if err != nil {
			return nil, err
		}
		return newTCPEndpoint(listener, route, manager.backends, manager.onError), nil
	case "udp":
		address := &net.UDPAddr{IP: net.ParseIP(route.ListenAddress), Port: route.ListenPort}
		listener, err := listenUDPInNamespace(route.ListenNamespacePID, address)
		if err != nil {
			return nil, err
		}
		return newUDPEndpoint(listener, route, manager.backends, manager.onError), nil
	default:
		return nil, errors.New("unsupported proxy protocol")
	}
}

func normalizeProtocol(protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return "tcp", nil
	case "udp":
		return "udp", nil
	default:
		return "", errors.New("proxy protocol must be tcp or udp")
	}
}

func normalizeListenAddress(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !address.Is4() {
		return "", errors.New("proxy listen address must be IPv4")
	}
	return address.String(), nil
}

func validPort(port int) bool {
	return port >= 1 && port <= 65_535
}

func routeKey(protocol, address string, port, namespacePID int) string {
	return strconv.Itoa(namespacePID) + ":" + protocol + ":" + net.JoinHostPort(address, strconv.Itoa(port))
}

func backendAddress(backend Backend) string {
	return net.JoinHostPort(backend.Address, strconv.Itoa(backend.Port))
}
