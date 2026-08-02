package portproxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iivankin/platformd/internal/trafficmetrics"
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
	flows     map[*tcpFlow]struct{}
	waitGroup sync.WaitGroup
	traffic   *trafficmetrics.Registry
}

type tcpFlow struct {
	mu        sync.Mutex
	inbound   net.Conn
	serviceID string
	traffic   *trafficmetrics.Registry
	ingress   uint64
	egress    uint64
	finished  bool
}

func newTCPEndpoint(listener net.Listener, route Route, backends BackendResolver, onError func(string, error), traffic *trafficmetrics.Registry) *tcpEndpoint {
	endpoint := &tcpEndpoint{
		listener: listener, backends: backends, onError: onError, traffic: traffic,
		capacity: make(chan struct{}, maximumTCPConnections), active: make(map[net.Conn]struct{}),
		flows: make(map[*tcpFlow]struct{}),
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
			serviceID := routeServiceID(*endpoint.route.Load())
			endpoint.traffic.StartTCP(serviceID)
			endpoint.track(connection, true)
			endpoint.waitGroup.Add(1)
			go endpoint.proxy(connection, serviceID)
		default:
			_ = connection.Close()
		}
	}
}

func (endpoint *tcpEndpoint) proxy(inbound net.Conn, serviceID string) {
	defer endpoint.waitGroup.Done()
	defer endpoint.traffic.FinishTCP(serviceID)
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

	var flow *tcpFlow
	if endpoint.traffic != nil && serviceID != "" {
		flow = &tcpFlow{inbound: inbound, serviceID: serviceID, traffic: endpoint.traffic}
		endpoint.trackFlow(flow, true)
		defer endpoint.trackFlow(flow, false)
	}

	type copyResult struct {
		ingress bool
		bytes   uint64
	}
	copyDone := make(chan copyResult, 2)
	copyStream := func(destination, source net.Conn, ingress bool) {
		count, _ := io.Copy(destination, source)
		if writable, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = writable.CloseWrite()
		}
		copyDone <- copyResult{ingress: ingress, bytes: uint64(count)}
	}
	go copyStream(outbound, inbound, true)
	go copyStream(inbound, outbound, false)
	first := <-copyDone
	second := <-copyDone
	var ingressBytes, egressBytes uint64
	for _, result := range [...]copyResult{first, second} {
		if result.ingress {
			ingressBytes = result.bytes
		} else {
			egressBytes = result.bytes
		}
	}
	if flow != nil {
		flow.finish(ingressBytes, egressBytes)
	}
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

func (endpoint *tcpEndpoint) trackFlow(flow *tcpFlow, add bool) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if add {
		endpoint.flows[flow] = struct{}{}
		return
	}
	delete(endpoint.flows, flow)
}

func (endpoint *tcpEndpoint) sampleTraffic() {
	endpoint.mu.Lock()
	flows := make([]*tcpFlow, 0, len(endpoint.flows))
	for flow := range endpoint.flows {
		flows = append(flows, flow)
	}
	endpoint.mu.Unlock()
	for _, flow := range flows {
		flow.sample()
	}
}

func (flow *tcpFlow) sample() {
	ingress, egress, ok := readTCPSocketCounters(flow.inbound)
	if ok {
		flow.record(ingress, egress)
	}
}

func (flow *tcpFlow) record(ingress, egress uint64) {
	flow.mu.Lock()
	if flow.finished {
		flow.mu.Unlock()
		return
	}
	ingressDelta := positiveDelta(ingress, flow.ingress)
	egressDelta := positiveDelta(egress, flow.egress)
	flow.ingress += ingressDelta
	flow.egress += egressDelta
	flow.mu.Unlock()
	flow.traffic.AddIngress(flow.serviceID, ingressDelta)
	flow.traffic.AddEgress(flow.serviceID, egressDelta)
}

func (flow *tcpFlow) finish(ingress, egress uint64) {
	flow.mu.Lock()
	if flow.finished {
		flow.mu.Unlock()
		return
	}
	ingressDelta := positiveDelta(ingress, flow.ingress)
	egressDelta := positiveDelta(egress, flow.egress)
	flow.ingress += ingressDelta
	flow.egress += egressDelta
	flow.finished = true
	flow.mu.Unlock()
	flow.traffic.AddIngress(flow.serviceID, ingressDelta)
	flow.traffic.AddEgress(flow.serviceID, egressDelta)
}

func positiveDelta(current, previous uint64) uint64 {
	if current <= previous {
		return 0
	}
	return current - previous
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
