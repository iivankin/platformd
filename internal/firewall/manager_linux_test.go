//go:build linux

package firewall

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

func TestCompileRulesetOwnsAllRequiredHooks(t *testing.T) {
	project := Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
	}
	compiled := compileRuleset(TableName, []Project{project})
	if compiled.table.Family != nftables.TableFamilyINet || len(compiled.chains) != 4 {
		t.Fatalf("unexpected table topology: %+v", compiled)
	}
	if compiled.chains[0].Hooknum != nftables.ChainHookInput || compiled.chains[0].Priority != nftables.ChainPriorityMangle {
		t.Fatalf("unexpected input chain: %+v", compiled.chains[0])
	}
	if compiled.chains[1].Hooknum != nftables.ChainHookForward || compiled.chains[1].Priority != nftables.ChainPriorityMangle {
		t.Fatalf("unexpected forward chain: %+v", compiled.chains[1])
	}
	if compiled.chains[2].Hooknum != nftables.ChainHookPrerouting || compiled.chains[2].Type != nftables.ChainTypeNAT {
		t.Fatalf("unexpected prerouting chain: %+v", compiled.chains[2])
	}
	if compiled.chains[3].Hooknum != nftables.ChainHookPostrouting || compiled.chains[3].Type != nftables.ChainTypeNAT {
		t.Fatalf("unexpected postrouting chain: %+v", compiled.chains[3])
	}

	var accepts, drops, masquerades int
	for _, currentRule := range compiled.rules {
		for _, expression := range currentRule.Exprs {
			switch value := expression.(type) {
			case *expr.Verdict:
				switch value.Kind {
				case expr.VerdictAccept:
					accepts++
				case expr.VerdictDrop:
					drops++
				}
			case *expr.Masq:
				masquerades++
			}
		}
	}
	if accepts == 0 || drops == 0 || masquerades != 1 {
		t.Fatalf("missing firewall verdicts: accepts=%d drops=%d masquerades=%d", accepts, drops, masquerades)
	}
	project.ObjectStoreEnabled = true
	withObjectStore := compileRuleset(TableName, []Project{project})
	if len(withObjectStore.rules) != len(compiled.rules)+2 {
		t.Fatalf("object store must add project and host TCP listener rules: without=%d with=%d", len(compiled.rules), len(withObjectStore.rules))
	}
	project.ServiceTelemetryEnabled = true
	withTelemetry := compileRuleset(TableName, []Project{project})
	if len(withTelemetry.rules) != len(withObjectStore.rules)+3 {
		t.Fatalf("service telemetry must add project Sentry/OTLP and host Sentry listeners: without=%d with=%d", len(withObjectStore.rules), len(withTelemetry.rules))
	}
	if countInputAcceptsForInterface(withTelemetry, loopbackInterface) != 2 {
		t.Fatal("gateway-backed port-forward targets are not reachable from the host namespace")
	}
	project.BlockedDatabaseEndpoints = []DatabaseEndpoint{{Address: netip.MustParseAddr("10.80.1.4"), Port: 5432}}
	withMaintenance := compileRuleset(TableName, []Project{project})
	if len(withMaintenance.rules) != len(withTelemetry.rules)+1 {
		t.Fatalf("database maintenance must add exactly one forward drop: without=%d with=%d", len(withTelemetry.rules), len(withMaintenance.rules))
	}
	project.GatewayListeners = []GatewayListener{{
		Address: netip.MustParseAddr("10.80.1.192"), Protocol: "tcp", Port: 5432,
	}}
	withGateway := compileRuleset(TableName, []Project{project})
	if len(withGateway.rules) != len(withMaintenance.rules)+2 {
		t.Fatalf("network gateway must add one exact accept and one cross-project drop: without=%d with=%d", len(withMaintenance.rules), len(withGateway.rules))
	}
	project.RemoteVIP = netip.MustParsePrefix("10.80.1.160/27")
	withRemoteVIP := compileRuleset(TableName, []Project{project})
	if len(withRemoteVIP.rules) != len(withGateway.rules)+2 {
		t.Fatalf("remote VIP must add tunnel accept and prerouting redirect: without=%d with=%d", len(withGateway.rules), len(withRemoteVIP.rules))
	}
}

func countInputAcceptsForInterface(compiled compiledRuleset, interfaceName string) int {
	expectedName := make([]byte, 16)
	copy(expectedName, interfaceName)
	count := 0
	for _, currentRule := range compiled.rules {
		if currentRule.Chain != compiled.chains[0] || !hasAcceptVerdict(currentRule) {
			continue
		}
		for index := 0; index+1 < len(currentRule.Exprs); index++ {
			metadata, metadataOK := currentRule.Exprs[index].(*expr.Meta)
			comparison, comparisonOK := currentRule.Exprs[index+1].(*expr.Cmp)
			if metadataOK && comparisonOK && metadata.Key == expr.MetaKeyIIFNAME &&
				comparison.Op == expr.CmpOpEq && bytes.Equal(comparison.Data, expectedName) {
				count++
				break
			}
		}
	}
	return count
}

func hasAcceptVerdict(rule *nftables.Rule) bool {
	for _, expression := range rule.Exprs {
		if value, ok := expression.(*expr.Verdict); ok && value.Kind == expr.VerdictAccept {
			return true
		}
	}
	return false
}

func TestCompileRulesetAddsPerServicePublicTrafficCounters(t *testing.T) {
	project := Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
		PublicTrafficEndpoints: []PublicTrafficEndpoint{{ServiceID: "api", Address: netip.MustParseAddr("10.80.1.8")}},
	}
	compiled := compileRuleset(TableName, []Project{project})
	if len(compiled.objects) != 2 {
		t.Fatalf("public counter objects = %d, want 2", len(compiled.objects))
	}
	want := map[string]bool{
		publicCounterName("ingress", "api"): false,
		publicCounterName("egress", "api"):  false,
	}
	for _, currentRule := range compiled.rules {
		for _, expression := range currentRule.Exprs {
			if reference, ok := expression.(*expr.Objref); ok {
				want[reference.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("counter %q is not referenced by a forward rule", name)
		}
	}
}

func TestSeedPublicTrafficCountersCarriesBytesAcrossRulesetReplacement(t *testing.T) {
	project := Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"), Gateway: netip.MustParseAddr("10.80.1.1"),
		PublicTrafficEndpoints: []PublicTrafficEndpoint{{ServiceID: "api", Address: netip.MustParseAddr("10.80.1.8")}},
	}
	compiled := compileRuleset(TableName, []Project{project})
	seedPublicTrafficCounters(compiled.objects, map[string]PublicTrafficCounters{
		"api": {IngressBytes: 12_345, EgressBytes: 67_890},
	})
	values := make(map[string]uint64)
	for _, object := range compiled.objects {
		if counter, ok := object.(*nftables.CounterObj); ok {
			values[counter.Name] = counter.Bytes
		}
	}
	if values[publicCounterName("ingress", "api")] != 12_345 ||
		values[publicCounterName("egress", "api")] != 67_890 {
		t.Fatalf("seeded counters = %+v", values)
	}
}

func TestEnableIPv4ForwardingAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enableIPv4ForwardingAt(path); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(path)
	if err != nil || string(value) != "1\n" {
		t.Fatalf("unexpected forwarding state %q: %v", value, err)
	}
}
