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
	onError   func(error)
	capacity  chan struct{}
	mu        sync.Mutex
	closed    bool
	active    map[net.Conn]struct{}
	waitGroup sync.WaitGroup
}

func newTCPEndpoint(listener net.Listener, route Route, backends BackendResolver, onError func(error)) *tcpEndpoint {
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
				endpoint.onError(err)
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

func (endpoint *tcpEndpoint) proxy(public net.Conn) {
	defer endpoint.waitGroup.Done()
	defer func() {
		endpoint.track(public, false)
		<-endpoint.capacity
		_ = public.Close()
	}()
	route := endpoint.route.Load()
	backend, available, err := endpoint.backends.ServiceBackend(route.ServiceID, route.TargetPort)
	if err != nil {
		endpoint.onError(err)
		return
	}
	if !available {
		return
	}
	private, err := net.DialTimeout("tcp", backendAddress(backend), 5*time.Second)
	if err != nil {
		endpoint.onError(err)
		return
	}
	endpoint.track(private, true)
	defer func() {
		endpoint.track(private, false)
		_ = private.Close()
	}()

	copyDone := make(chan struct{}, 2)
	copyStream := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		if writable, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = writable.CloseWrite()
		}
		copyDone <- struct{}{}
	}
	go copyStream(private, public)
	go copyStream(public, private)
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
