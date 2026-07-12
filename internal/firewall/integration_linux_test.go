//go:build linux && integration

package firewall

import (
	"net/netip"
	"os"
	"testing"

	"github.com/google/nftables"
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
