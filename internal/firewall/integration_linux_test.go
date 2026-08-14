//go:build linux && integration

package firewall

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
)

func TestManagerPublishesAndReplacesSingleTable(t *testing.T) {
	if os.Getenv("PLATFORMD_FIREWALL_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_FIREWALL_INTEGRATION=1 on an isolated root host")
	}
	manager := New()
	t.Cleanup(func() {
		if err := manager.Clear(); err != nil {
			t.Errorf("clear firewall: %v", err)
		}
	})
	if err := manager.Probe(); err != nil {
		t.Fatalf("probe nf_tables: %v", err)
	}
	if err := EnableIPv4Forwarding(); err != nil {
		t.Fatalf("enable forwarding: %v", err)
	}

	first := Project{ID: "first", Bridge: "pd-first", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1")}
	if err := manager.Apply([]Project{first}); err != nil {
		t.Fatalf("apply first ruleset: %v", err)
	}
	assertSinglePlatformTable(t)

	second := Project{ID: "second", Bridge: "pd-second", Subnet: netip.MustParsePrefix("10.80.2.0/24"), Gateway: netip.MustParseAddr("10.80.2.1")}
	if err := manager.Apply([]Project{second}); err != nil {
		t.Fatalf("replace ruleset: %v", err)
	}
	assertSinglePlatformTable(t)

	if err := manager.Clear(); err != nil {
		t.Fatalf("clear ruleset: %v", err)
	}
	connection := &nftables.Conn{}
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table.Name == TableName {
			t.Fatal("platform firewall table survived clear")
		}
	}
}

func TestManagerAllowsHostPortForwardsToGatewayBackedTargets(t *testing.T) {
	if os.Getenv("PLATFORMD_FIREWALL_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_FIREWALL_INTEGRATION=1 on an isolated root host")
	}
	bridge := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: "pdhlooptest"}}
	if err := netlink.LinkAdd(bridge); err != nil {
		t.Fatalf("create test bridge: %v", err)
	}
	t.Cleanup(func() {
		if err := netlink.LinkDel(bridge); err != nil {
			t.Errorf("delete test bridge: %v", err)
		}
	})
	address, err := netlink.ParseAddr("10.89.250.1/24")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.AddrAdd(bridge, address); err != nil {
		t.Fatalf("assign test gateway: %v", err)
	}
	if err := netlink.LinkSetUp(bridge); err != nil {
		t.Fatalf("bring test bridge up: %v", err)
	}

	listeners := make([]net.Listener, 0, 2)
	for _, port := range []uint16{ObjectStorePort, ServiceTelemetryPort} {
		listener, listenErr := net.Listen("tcp4", fmt.Sprintf("10.89.250.1:%d", port))
		if listenErr != nil {
			t.Fatalf("listen on gateway port %d: %v", port, listenErr)
		}
		listeners = append(listeners, listener)
	}
	t.Cleanup(func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil {
				t.Errorf("close test listener: %v", err)
			}
		}
	})

	manager := New()
	t.Cleanup(func() {
		if err := manager.Clear(); err != nil {
			t.Errorf("clear firewall: %v", err)
		}
	})
	project := Project{
		ID: "host-forward", Bridge: bridge.Attrs().Name,
		Subnet: netip.MustParsePrefix("10.89.250.0/24"), Gateway: netip.MustParseAddr("10.89.250.1"),
		ObjectStoreEnabled: true, ServiceTelemetryEnabled: true,
	}
	if err := manager.Apply([]Project{project}); err != nil {
		t.Fatalf("apply test ruleset: %v", err)
	}
	for _, port := range []uint16{ObjectStorePort, ServiceTelemetryPort} {
		connection, dialErr := net.DialTimeout("tcp4", fmt.Sprintf("10.89.250.1:%d", port), time.Second)
		if dialErr != nil {
			t.Fatalf("host connection to gateway port %d: %v", port, dialErr)
		}
		if err := connection.Close(); err != nil {
			t.Fatalf("close gateway connection: %v", err)
		}
	}
}

func TestManagerPreservesPublicTrafficAcrossRulesetReplacement(t *testing.T) {
	if os.Getenv("PLATFORMD_FIREWALL_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_FIREWALL_INTEGRATION=1 on an isolated root host")
	}
	manager := New()
	t.Cleanup(func() {
		if err := manager.Clear(); err != nil {
			t.Errorf("clear firewall: %v", err)
		}
	})
	manager.trafficCarry["api"] = PublicTrafficCounters{IngressBytes: 12_345, EgressBytes: 67_890}
	project := Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
		PublicTrafficEndpoints: []PublicTrafficEndpoint{{ServiceID: "api", Address: netip.MustParseAddr("10.80.1.8")}},
	}
	if err := manager.Apply([]Project{project}); err != nil {
		t.Fatalf("apply seeded ruleset: %v", err)
	}
	assertPublicTraffic(t, manager, "api", 12_345, 67_890)
	project.ObjectStoreEnabled = true
	if err := manager.Apply([]Project{project}); err != nil {
		t.Fatalf("replace seeded ruleset: %v", err)
	}
	assertPublicTraffic(t, manager, "api", 12_345, 67_890)
}

func assertPublicTraffic(t *testing.T, manager *Manager, serviceID string, ingress, egress uint64) {
	t.Helper()
	counters, err := manager.PublicTraffic()
	if err != nil {
		t.Fatal(err)
	}
	if counters[serviceID].IngressBytes != ingress || counters[serviceID].EgressBytes != egress {
		t.Fatalf("public traffic = %+v", counters)
	}
}

func assertSinglePlatformTable(t *testing.T) {
	t.Helper()
	connection := &nftables.Conn{}
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, table := range tables {
		if table.Name == TableName {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one platform firewall table, got %d", count)
	}
}
