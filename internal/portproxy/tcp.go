package portproxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const maximumTCPConnections = 1024

type tcpEndpoint struct {
	listener  net.Listener
	route     atomic.Pointer[Route]
	backends  BackendResolver
	onError   func(string, error)
	capacity  chan struct{}
	mu        sync.Mutex
	closed    bool
	active    map[net.Conn]struct{}
	waitGroup sync.WaitGroup
}

func newTCPEndpoint(listener net.Listener, route Route, backends BackendResolver, onError func(string, error)) *tcpEndpoint {
	endpoint := &tcpEndpoint{
		listener: listener, backends: backends, onError: onError,
		capacity: make(chan struct{}, maximumTCPConnections), active: make(map[net.Conn]struct{}),
	}
	endpoint.route.Store(&route)
	endpoint.waitGroup.Add(1)
	go endpoint.accept()
	return endpoint
}

func (endpoint *tcpEndpoint) accept() {
	defer endpoint.waitGroup.Done()
	for {
		connection, err := endpoint.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				endpoint.onError(endpoint.route.Load().ID, err)
			}
			return
		}
		select {
		case endpoint.capacity <- struct{}{}:
			endpoint.track(connection, true)
			endpoint.waitGroup.Add(1)
			go endpoint.proxy(connection)
		default:
			_ = connection.Close()
		}
	}
}

func (endpoint *tcpEndpoint) proxy(inbound net.Conn) {
	defer endpoint.waitGroup.Done()
	defer func() {
		endpoint.track(inbound, false)
		<-endpoint.capacity
		_ = inbound.Close()
	}()
	route := endpoint.route.Load()
	backend, available, err := route.Target.Resolve(endpoint.backends)
	if err != nil {
		endpoint.onError(route.ID, err)
		return
	}
	if !available {
		return
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	if backend.SourceAddress != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(backend.SourceAddress)}
	}
	outbound, err := dialTCPInNamespace(route.DialNamespacePID, dialer, backendAddress(backend))
	if err != nil {
		endpoint.onError(route.ID, err)
		return
	}
	endpoint.track(outbound, true)
	defer func() {
		endpoint.track(outbound, false)
		_ = outbound.Close()
	}()

	copyDone := make(chan struct{}, 2)
	copyStream := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		if writable, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = writable.CloseWrite()
		}
		copyDone <- struct{}{}
	}
	go copyStream(outbound, inbound)
	go copyStream(inbound, outbound)
	<-copyDone
	<-copyDone
}

func (endpoint *tcpEndpoint) Update(route Route) {
	endpoint.route.Store(&route)
}

func (endpoint *tcpEndpoint) track(connection net.Conn, add bool) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if add {
		if endpoint.closed {
			_ = connection.Close()
			return
		}
		endpoint.active[connection] = struct{}{}
		return
	}
	delete(endpoint.active, connection)
}

func (endpoint *tcpEndpoint) Close() error {
	endpoint.mu.Lock()
	if endpoint.closed {
		endpoint.mu.Unlock()
		return nil
	}
	endpoint.closed = true
	connections := make([]net.Conn, 0, len(endpoint.active))
	for connection := range endpoint.active {
		connections = append(connections, connection)
	}
	endpoint.mu.Unlock()
	failures := []error{endpoint.listener.Close()}
	for _, connection := range connections {
		failures = append(failures, connection.Close())
	}
	endpoint.waitGroup.Wait()
	return errors.Join(failures...)
}
