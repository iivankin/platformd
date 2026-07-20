package portproxy

import (
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/deployment"
)

type resolverStub struct {
	backend deployment.Backend
	mu      sync.Mutex
	ports   []int
}

func (resolver *resolverStub) ServiceBackend(_ string, targetPort int) (deployment.Backend, bool, error) {
	resolver.mu.Lock()
	resolver.ports = append(resolver.ports, targetPort)
	resolver.mu.Unlock()
	return resolver.backend, true, nil
}

func (resolver *resolverStub) requestedPorts() []int {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return append([]int(nil), resolver.ports...)
}

func TestManagerForwardsTCPAndUpdatesTargetWithoutRebinding(t *testing.T) {
	backend := tcpEchoServer(t)
	host, port := splitAddress(t, backend.Addr().String())
	resolver := &resolverStub{backend: deployment.Backend{DeploymentID: "deployment", Address: host, Port: port}}
	manager, err := New(Config{Backends: resolver})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	publicPort := availableTCPPort(t)
	if err := manager.Add(Route{ID: "route", Protocol: "TCP", ListenAddress: "127.0.0.1", ListenPort: publicPort, Target: ServiceTarget{ServiceID: "service", Port: 8080}}); err != nil {
		t.Fatal(err)
	}
	assertTCPEcho(t, publicPort, "first")
	if err := manager.Add(Route{ID: "route", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: publicPort, Target: ServiceTarget{ServiceID: "service", Port: 9090}}); err != nil {
		t.Fatal(err)
	}
	assertTCPEcho(t, publicPort, "second")
	ports := resolver.requestedPorts()
	if len(ports) != 2 || ports[0] != 8080 || ports[1] != 9090 {
		t.Fatalf("resolved target ports = %v", ports)
	}
}

func TestManagerForwardsUDPAndRejectsUnavailableOrReservedTCPPorts(t *testing.T) {
	backend := udpEchoServer(t)
	host, port := splitAddress(t, backend.LocalAddr().String())
	resolver := &resolverStub{backend: deployment.Backend{DeploymentID: "deployment", Address: host, Port: port}}
	manager, err := New(Config{Backends: resolver})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	publicPort := availableUDPPort(t)
	if err := manager.Add(Route{ID: "route", Protocol: "udp", ListenAddress: "127.0.0.1", ListenPort: publicPort, Target: ServiceTarget{ServiceID: "service", Port: 5353}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: publicPort})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("datagram")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 32)
	count, err := client.Read(buffer)
	if err != nil || string(buffer[:count]) != "datagram" {
		t.Fatalf("UDP echo = %q, %v", buffer[:count], err)
	}

	occupied, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	_, occupiedPort := splitAddress(t, occupied.Addr().String())
	if err := manager.Add(Route{ID: "occupied-tcp", Protocol: "tcp", ListenAddress: "0.0.0.0", ListenPort: occupiedPort, Target: ServiceTarget{ServiceID: "service", Port: 8080}}); err == nil {
		t.Fatal("occupied public TCP port was accepted")
	}
	occupiedUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer occupiedUDP.Close()
	_, occupiedUDPPort := splitAddress(t, occupiedUDP.LocalAddr().String())
	if err := manager.Add(Route{ID: "occupied-udp", Protocol: "udp", ListenAddress: "0.0.0.0", ListenPort: occupiedUDPPort, Target: ServiceTarget{ServiceID: "service", Port: 5353}}); err == nil {
		t.Fatal("occupied public UDP port was accepted")
	}
	if err := manager.Add(Route{ID: "reserved-https", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: 443, Target: ServiceTarget{ServiceID: "service", Port: 8443}}); err == nil {
		t.Fatal("reserved HTTPS port was accepted")
	}
}

func TestManagerForwardsToAddressTargetWithPinnedSource(t *testing.T) {
	manager, err := New(Config{Backends: &resolverStub{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	tcpBackend := tcpEchoServer(t)
	host, port := splitAddress(t, tcpBackend.Addr().String())
	listenPort := availableTCPPort(t)
	if err := manager.Add(Route{
		ID: "tcp-gateway", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: listenPort,
		Target: AddressTarget{Host: host, Port: port, SourceAddress: "127.0.0.1"},
	}); err != nil {
		t.Fatal(err)
	}
	assertTCPEcho(t, listenPort, "gateway")

	udpBackend := udpEchoServer(t)
	host, port = splitAddress(t, udpBackend.LocalAddr().String())
	listenPort = availableUDPPort(t)
	if err := manager.Add(Route{
		ID: "udp-gateway", Protocol: "udp", ListenAddress: "127.0.0.1", ListenPort: listenPort,
		Target: AddressTarget{Host: host, Port: port, SourceAddress: "127.0.0.1"},
	}); err != nil {
		t.Fatal(err)
	}
	assertUDPEcho(t, listenPort, "gateway")
}

func TestManagerRejectsNegativeNamespacePIDsAndSeparatesListenerOwnership(t *testing.T) {
	manager, err := New(Config{Backends: &resolverStub{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	target := AddressTarget{Host: "127.0.0.1", Port: 8080}
	if err := manager.Add(Route{
		ID: "negative-listener", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: 9000,
		ListenNamespacePID: -1, Target: target,
	}); err == nil {
		t.Fatal("negative listener namespace PID was accepted")
	}
	if err := manager.Add(Route{
		ID: "negative-dial", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: 9000,
		DialNamespacePID: -1, Target: target,
	}); err == nil {
		t.Fatal("negative dial namespace PID was accepted")
	}
	if routeKey("tcp", "127.0.0.1", 9000, 101) == routeKey("tcp", "127.0.0.1", 9000, 202) {
		t.Fatal("listener ownership collides across network namespaces")
	}
}

func tcpEchoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener
}

func udpEchoServer(t *testing.T) *net.UDPConn {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	go func() {
		buffer := make([]byte, maximumUDPPacketBytes)
		for {
			count, address, readErr := connection.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = connection.WriteToUDP(buffer[:count], address)
		}
	}()
	return connection
}

func assertTCPEcho(t *testing.T, port int, value string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(value))
	if _, err := io.ReadFull(connection, buffer); err != nil || string(buffer) != value {
		t.Fatalf("TCP echo = %q, %v", buffer, err)
	}
}

func assertUDPEcho(t *testing.T, port int, value string) {
	t.Helper()
	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(value))
	count, err := client.Read(buffer)
	if err != nil || string(buffer[:count]) != value {
		t.Fatalf("UDP echo = %q, %v", buffer[:count], err)
	}
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port := splitAddress(t, listener.Addr().String())
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func availableUDPPort(t *testing.T) int {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	_, port := splitAddress(t, connection.LocalAddr().String())
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func splitAddress(t *testing.T, address string) (string, int) {
	t.Helper()
	host, portValue, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
