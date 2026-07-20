package firewall

import (
	"net/netip"
	"testing"
)

func TestCanonicalProjectsSortsAndMasks(t *testing.T) {
	projects, err := canonicalProjects([]Project{
		{
			ID: "z", Bridge: "pd-z", Subnet: netip.MustParsePrefix("10.80.2.9/24"), Gateway: netip.MustParseAddr("10.80.2.1"),
			BlockedDatabaseEndpoints: []DatabaseEndpoint{
				{Address: netip.MustParseAddr("10.80.2.9"), Port: 6379},
				{Address: netip.MustParseAddr("10.80.2.8"), Port: 5432},
			},
		},
		{ID: "a", Bridge: "pd-a", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].ID != "a" || projects[1].Subnet.String() != "10.80.2.0/24" ||
		projects[1].BlockedDatabaseEndpoints[0].Address.String() != "10.80.2.8" {
		t.Fatalf("unexpected canonical projects: %+v", projects)
	}
}

func TestCanonicalProjectsRejectsUnsafeTopology(t *testing.T) {
	tests := map[string][]Project{
		"duplicate bridge": {
			{ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1")},
			{ID: "b", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.2.0/24"), Gateway: netip.MustParseAddr("10.80.2.1")},
		},
		"overlapping subnet": {
			{ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.0.0/16"), Gateway: netip.MustParseAddr("10.80.0.1")},
			{ID: "b", Bridge: "pd1", Subnet: netip.MustParsePrefix("10.80.2.0/24"), Gateway: netip.MustParseAddr("10.80.2.1")},
		},
		"network gateway": {
			{ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.0")},
		},
		"database outside project": {
			{
				ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
				BlockedDatabaseEndpoints: []DatabaseEndpoint{{Address: netip.MustParseAddr("10.80.2.4"), Port: 5432}},
			},
		},
		"duplicate database endpoint": {
			{
				ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
				BlockedDatabaseEndpoints: []DatabaseEndpoint{
					{Address: netip.MustParseAddr("10.80.1.4"), Port: 5432},
					{Address: netip.MustParseAddr("10.80.1.4"), Port: 5432},
				},
			},
		},
		"gateway listener outside project": {
			{
				ID: "a", Bridge: "pd0", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
				GatewayListeners: []GatewayListener{{Address: netip.MustParseAddr("10.80.2.192"), Protocol: "tcp", Port: 5432}},
			},
		},
	}
	for name, projects := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := canonicalProjects(projects); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
