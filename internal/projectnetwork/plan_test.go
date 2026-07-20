package projectnetwork

import (
	"errors"
	"net/netip"
	"testing"
)

func TestPlanIsStableAndSkipsOccupiedRoutes(t *testing.T) {
	projects := []Project{{ID: "z-project", Name: "z"}, {ID: "a-project", Name: "a"}}
	result, err := planWithPool(projects, []netip.Prefix{netip.MustParsePrefix("10.80.0.0/23")}, netip.MustParsePrefix("10.80.0.0/16"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 0 || len(result.Assignments) != 2 {
		t.Fatalf("unexpected plan: %+v", result)
	}
	if result.Assignments[0].ProjectID != "a-project" || result.Assignments[0].Subnet.String() != "10.80.2.0/24" || result.Assignments[1].Subnet.String() != "10.80.3.0/24" {
		t.Fatalf("unexpected assignments: %+v", result.Assignments)
	}
	if result.Assignments[0].Gateway.String() != "10.80.2.1" || len(result.Assignments[0].Bridge) != 15 {
		t.Fatalf("unexpected gateway or bridge: %+v", result.Assignments[0])
	}

	reversed, err := planWithPool([]Project{projects[1], projects[0]}, []netip.Prefix{netip.MustParsePrefix("10.80.0.0/23")}, netip.MustParsePrefix("10.80.0.0/16"))
	if err != nil || reversed.Assignments[0] != result.Assignments[0] || reversed.Assignments[1] != result.Assignments[1] {
		t.Fatalf("project input order changed plan: %+v, %v", reversed, err)
	}
}

func TestPlanReportsOnlyProjectsBeyondCapacity(t *testing.T) {
	projects := make([]Project, 0, 257)
	for index := range 257 {
		projects = append(projects, Project{ID: string(rune(0x1000 + index)), Name: "project"})
	}
	result, err := planWithPool(projects, nil, netip.MustParsePrefix("10.80.0.0/16"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 256 || len(result.Failures) != 1 || !errors.Is(result.Failures[0].Err, ErrPoolExhausted) {
		t.Fatalf("unexpected exhaustion result: assignments=%d failures=%+v", len(result.Assignments), result.Failures)
	}
}

func TestPlanRejectsInvalidTopologyInputs(t *testing.T) {
	for _, test := range []struct {
		projects []Project
		occupied []netip.Prefix
		pool     netip.Prefix
	}{
		{projects: []Project{{ID: "", Name: "name"}}, pool: netip.MustParsePrefix("10.80.0.0/16")},
		{projects: []Project{{ID: "same", Name: "a"}, {ID: "same", Name: "b"}}, pool: netip.MustParsePrefix("10.80.0.0/16")},
		{projects: []Project{{ID: "a", Name: "a"}}, occupied: []netip.Prefix{netip.MustParsePrefix("2001:db8::/64")}, pool: netip.MustParsePrefix("10.80.0.0/16")},
		{projects: []Project{{ID: "a", Name: "a"}}, pool: netip.MustParsePrefix("100.64.0.0/10")},
	} {
		if _, err := planWithPool(test.projects, test.occupied, test.pool); err == nil {
			t.Fatalf("expected invalid input to fail: %+v", test)
		}
	}
}

func TestHostAddressKeepsContainerAndGatewayRangesDisjoint(t *testing.T) {
	subnet := netip.MustParsePrefix("10.80.42.0/24")
	containerEnd, err := HostAddress(subnet, ContainerLeaseLastHost)
	if err != nil {
		t.Fatal(err)
	}
	gatewayStart, err := HostAddress(subnet, GatewayFirstHost)
	if err != nil {
		t.Fatal(err)
	}
	if containerEnd.String() != "10.80.42.191" || gatewayStart.String() != "10.80.42.192" {
		t.Fatalf("unexpected reserved ranges: container=%s gateway=%s", containerEnd, gatewayStart)
	}
}
