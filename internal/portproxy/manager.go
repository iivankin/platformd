package portproxy

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/deployment"
)

type BackendResolver interface {
	ServiceBackend(string, int) (deployment.Backend, bool, error)
}

type Route struct {
	Protocol   string
	PublicPort int
	ServiceID  string
	TargetPort int
}

type Config struct {
	Backends BackendResolver
	OnError  func(error)
}

type endpoint interface {
	Close() error
	Update(Route)
}

type Manager struct {
	backends  BackendResolver
	onError   func(error)
	mu        sync.Mutex
	endpoints map[string]endpoint
}

func New(config Config) (*Manager, error) {
	if config.Backends == nil {
		return nil, errors.New("public port proxy requires a backend resolver")
	}
	onError := config.OnError
	if onError == nil {
		onError = func(error) {}
	}
	return &Manager{
		backends: config.Backends, onError: onError,
		endpoints: make(map[string]endpoint),
	}, nil
}

func (manager *Manager) Add(route Route) error {
	protocol, err := normalizeProtocol(route.Protocol)
	if err != nil {
		return err
	}
	if route.ServiceID == "" || !validPort(route.PublicPort) || !validPort(route.TargetPort) {
		return errors.New("public listener route is incomplete")
	}
	if protocol == "tcp" && route.PublicPort == 443 {
		return errors.New("public TCP port 443 is reserved by platformd HTTPS ingress")
	}
	route.Protocol = protocol
	key := routeKey(route.Protocol, route.PublicPort)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, exists := manager.endpoints[key]; exists {
		current.Update(route)
		return nil
	}
	created, err := manager.listen(route)
	if err != nil {
		return fmt.Errorf("listen on public %s port %d: %w", route.Protocol, route.PublicPort, err)
	}
	manager.endpoints[key] = created
	return nil
}

func (manager *Manager) Remove(protocol string, publicPort int) error {
	normalized, err := normalizeProtocol(protocol)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	current, exists := manager.endpoints[routeKey(normalized, publicPort)]
	if exists {
		delete(manager.endpoints, routeKey(normalized, publicPort))
	}
	manager.mu.Unlock()
	if !exists {
		return nil
	}
	return current.Close()
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	current := make([]endpoint, 0, len(manager.endpoints))
	for _, item := range manager.endpoints {
		current = append(current, item)
	}
	manager.endpoints = make(map[string]endpoint)
	manager.mu.Unlock()
	var failures []error
	for _, item := range current {
		failures = append(failures, item.Close())
	}
	return errors.Join(failures...)
}

func (manager *Manager) listen(route Route) (endpoint, error) {
	switch route.Protocol {
	case "tcp":
		listener, err := net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(route.PublicPort))
		if err != nil {
			return nil, err
		}
		return newTCPEndpoint(listener, route, manager.backends, manager.onError), nil
	case "udp":
		listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: route.PublicPort})
		if err != nil {
			return nil, err
		}
		return newUDPEndpoint(listener, route, manager.backends, manager.onError), nil
	default:
		return nil, errors.New("unsupported public listener protocol")
	}
}

func normalizeProtocol(protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return "tcp", nil
	case "udp":
		return "udp", nil
	default:
		return "", errors.New("public listener protocol must be tcp or udp")
	}
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func routeKey(protocol string, publicPort int) string {
	return protocol + ":" + strconv.Itoa(publicPort)
}

func backendAddress(backend deployment.Backend) string {
	return net.JoinHostPort(backend.Address, strconv.Itoa(backend.Port))
}
