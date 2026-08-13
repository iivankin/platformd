//go:build linux

package telemetry

import (
	"net/netip"
	"testing"
)

func TestServiceGatewayBindsBeforeProjectBridgeExists(t *testing.T) {
	listener, err := listenServiceGateway(netip.MustParseAddr("192.0.2.1"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
