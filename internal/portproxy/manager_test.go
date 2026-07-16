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
	if err := manager.Add(Route{Protocol: "TCP", PublicPort: publicPort, ServiceID: "service", TargetPort: 8080}); err != nil {
		t.Fatal(err)
	}
	assertTCPEcho(t, publicPort, "first")
	if err := manager.Add(Route{Protocol: "tcp", PublicPort: publicPort, ServiceID: "service", TargetPort: 9090}); err != nil {
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
	if err := manager.Add(Route{Protocol: "udp", PublicPort: publicPort, ServiceID: "service", TargetPort: 5353}); err != nil {
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
	if err := manager.Add(Route{Protocol: "tcp", PublicPort: occupiedPort, ServiceID: "service", TargetPort: 8080}); err == nil {
		t.Fatal("occupied public TCP port was accepted")
	}
	occupiedUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer occupiedUDP.Close()
	_, occupiedUDPPort := splitAddress(t, occupiedUDP.LocalAddr().String())
	if err := manager.Add(Route{Protocol: "udp", PublicPort: occupiedUDPPort, ServiceID: "service", TargetPort: 5353}); err == nil {
		t.Fatal("occupied public UDP port was accepted")
	}
	if err := manager.Add(Route{Protocol: "TCP", PublicPort: 443, ServiceID: "service", TargetPort: 8443}); err == nil {
		t.Fatal("reserved HTTPS port was accepted")
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
