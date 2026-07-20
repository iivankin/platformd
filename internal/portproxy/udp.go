package portproxy

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maximumUDPPacketBytes = 65_535
	maximumUDPSessions    = 1024
	udpSessionIdle        = 60 * time.Second
	udpCleanupPeriod      = 15 * time.Second
)

type udpSession struct {
	backendID string
	client    *net.UDPAddr
	private   *net.UDPConn
	lastSeen  time.Time
}

type udpEndpoint struct {
	listener  *net.UDPConn
	route     atomic.Pointer[Route]
	backends  BackendResolver
	onError   func(string, error)
	mu        sync.Mutex
	closed    bool
	done      chan struct{}
	capacity  chan struct{}
	sessions  map[string]*udpSession
	waitGroup sync.WaitGroup
}

func newUDPEndpoint(listener *net.UDPConn, route Route, backends BackendResolver, onError func(string, error)) *udpEndpoint {
	endpoint := &udpEndpoint{
		listener: listener, backends: backends, onError: onError,
		done: make(chan struct{}), capacity: make(chan struct{}, maximumUDPSessions),
		sessions: make(map[string]*udpSession),
	}
	endpoint.route.Store(&route)
	endpoint.waitGroup.Add(2)
	go endpoint.readPublic()
	go endpoint.cleanIdle()
	return endpoint
}

func (endpoint *udpEndpoint) readPublic() {
	defer endpoint.waitGroup.Done()
	for {
		buffer := make([]byte, maximumUDPPacketBytes)
		count, client, err := endpoint.listener.ReadFromUDP(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				endpoint.onError(endpoint.route.Load().ID, err)
			}
			return
		}
		packet := append([]byte(nil), buffer[:count]...)
		select {
		case endpoint.capacity <- struct{}{}:
			endpoint.waitGroup.Add(1)
			go endpoint.forward(client, packet)
		default:
			// UDP has no backpressure signal. Dropping excess datagrams keeps a
			// network flood from creating an unbounded number of goroutines.
		}
	}
}

func (endpoint *udpEndpoint) forward(client *net.UDPAddr, packet []byte) {
	defer endpoint.waitGroup.Done()
	defer func() { <-endpoint.capacity }()
	route := endpoint.route.Load()
	backend, available, err := route.Target.Resolve(endpoint.backends)
	if err != nil {
		endpoint.onError(route.ID, err)
		return
	}
	if !available {
		return
	}
	address := backendAddress(backend)
	backendID := backend.ID + "@" + address + "@" + backend.SourceAddress + "@" + fmt.Sprint(route.DialNamespacePID)
	session, err := endpoint.session(client, backendID, address, backend.SourceAddress, route.DialNamespacePID)
	if err != nil {
		endpoint.onError(route.ID, err)
		return
	}
	if _, err := session.private.Write(packet); err != nil && !errors.Is(err, net.ErrClosed) {
		endpoint.onError(route.ID, err)
	}
}

func (endpoint *udpEndpoint) Update(route Route) {
	endpoint.route.Store(&route)
}

func (endpoint *udpEndpoint) session(client *net.UDPAddr, backendID, address, sourceAddress string, namespacePID int) (*udpSession, error) {
	key := client.String()
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if endpoint.closed {
		return nil, net.ErrClosed
	}
	if current := endpoint.sessions[key]; current != nil && current.backendID == backendID {
		current.lastSeen = time.Now()
		return current, nil
	} else if current != nil {
		_ = current.private.Close()
		delete(endpoint.sessions, key)
	}
	if len(endpoint.sessions) >= maximumUDPSessions {
		endpoint.evictOldestLocked()
	}
	backend, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	var source *net.UDPAddr
	if sourceAddress != "" {
		source = &net.UDPAddr{IP: net.ParseIP(sourceAddress)}
	}
	private, err := dialUDPInNamespace(namespacePID, source, backend)
	if err != nil {
		return nil, err
	}
	created := &udpSession{
		backendID: backendID, client: cloneUDPAddress(client), private: private, lastSeen: time.Now(),
	}
	endpoint.sessions[key] = created
	endpoint.waitGroup.Add(1)
	go endpoint.readPrivate(key, created)
	return created, nil
}

func (endpoint *udpEndpoint) readPrivate(key string, session *udpSession) {
	defer endpoint.waitGroup.Done()
	buffer := make([]byte, maximumUDPPacketBytes)
	for {
		count, err := session.private.Read(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				endpoint.onError(endpoint.route.Load().ID, err)
			}
			return
		}
		if _, err := endpoint.listener.WriteToUDP(buffer[:count], session.client); err != nil {
			if !errors.Is(err, net.ErrClosed) {
				endpoint.onError(endpoint.route.Load().ID, err)
			}
			return
		}
		endpoint.mu.Lock()
		if endpoint.sessions[key] == session {
			session.lastSeen = time.Now()
		}
		endpoint.mu.Unlock()
	}
}

func (endpoint *udpEndpoint) cleanIdle() {
	defer endpoint.waitGroup.Done()
	ticker := time.NewTicker(udpCleanupPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-endpoint.done:
			return
		case <-ticker.C:
		}
		endpoint.mu.Lock()
		if endpoint.closed {
			endpoint.mu.Unlock()
			return
		}
		cutoff := time.Now().Add(-udpSessionIdle)
		for key, session := range endpoint.sessions {
			if session.lastSeen.Before(cutoff) {
				_ = session.private.Close()
				delete(endpoint.sessions, key)
			}
		}
		endpoint.mu.Unlock()
	}
}

func (endpoint *udpEndpoint) evictOldestLocked() {
	var oldestKey string
	var oldest *udpSession
	for key, session := range endpoint.sessions {
		if oldest == nil || session.lastSeen.Before(oldest.lastSeen) {
			oldestKey = key
			oldest = session
		}
	}
	if oldest != nil {
		_ = oldest.private.Close()
		delete(endpoint.sessions, oldestKey)
	}
}

func (endpoint *udpEndpoint) Close() error {
	endpoint.mu.Lock()
	if endpoint.closed {
		endpoint.mu.Unlock()
		return nil
	}
	endpoint.closed = true
	close(endpoint.done)
	sessions := make([]*udpSession, 0, len(endpoint.sessions))
	for _, session := range endpoint.sessions {
		sessions = append(sessions, session)
	}
	endpoint.sessions = make(map[string]*udpSession)
	endpoint.mu.Unlock()
	failures := []error{endpoint.listener.Close()}
	for _, session := range sessions {
		failures = append(failures, session.private.Close())
	}
	endpoint.waitGroup.Wait()
	return errors.Join(failures...)
}

func cloneUDPAddress(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{
		IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone,
	}
}
