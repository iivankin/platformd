//go:build linux

package portproxy

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

func TestManagerSamplesOpenTCPFlow(t *testing.T) {
	backend := tcpEchoServer(t)
	host, port := splitAddress(t, backend.Addr().String())
	traffic := trafficmetrics.NewRegistry()
	manager, err := New(Config{
		Backends: &resolverStub{backend: deployment.Backend{
			DeploymentID: "deployment", Address: host, Port: port,
		}},
		Traffic: traffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	publicPort := availableTCPPort(t)
	if err := manager.Add(Route{
		ID: "route", Protocol: "tcp", ListenAddress: "127.0.0.1", ListenPort: publicPort,
		Target: ServiceTarget{ServiceID: "service", Port: 8080},
	}); err != nil {
		t.Fatal(err)
	}

	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := []byte("sample while the flow is open")
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		manager.SampleTCP()
		counters := traffic.Snapshot()["service"]
		if counters.IngressBytes == uint64(len(payload)) && counters.EgressBytes == uint64(len(payload)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open TCP flow counters = %+v", counters)
		}
		time.Sleep(time.Millisecond)
	}
}
