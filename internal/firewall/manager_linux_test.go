//go:build linux

package firewall

import (
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
	if compiled.table.Family != nftables.TableFamilyINet || len(compiled.chains) != 3 {
		t.Fatalf("unexpected table topology: %+v", compiled)
	}
	if compiled.chains[0].Hooknum != nftables.ChainHookInput || compiled.chains[0].Priority != nftables.ChainPriorityMangle {
		t.Fatalf("unexpected input chain: %+v", compiled.chains[0])
	}
	if compiled.chains[1].Hooknum != nftables.ChainHookForward || compiled.chains[1].Priority != nftables.ChainPriorityMangle {
		t.Fatalf("unexpected forward chain: %+v", compiled.chains[1])
	}
	if compiled.chains[2].Hooknum != nftables.ChainHookPostrouting || compiled.chains[2].Type != nftables.ChainTypeNAT {
		t.Fatalf("unexpected postrouting chain: %+v", compiled.chains[2])
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
	if len(withObjectStore.rules) != len(compiled.rules)+1 {
		t.Fatalf("object store must add exactly one TCP listener rule: without=%d with=%d", len(compiled.rules), len(withObjectStore.rules))
	}
	project.BlockedDatabaseEndpoints = []DatabaseEndpoint{{Address: netip.MustParseAddr("10.80.1.4"), Port: 5432}}
	withMaintenance := compileRuleset(TableName, []Project{project})
	if len(withMaintenance.rules) != len(withObjectStore.rules)+1 {
		t.Fatalf("database maintenance must add exactly one forward drop: without=%d with=%d", len(withObjectStore.rules), len(withMaintenance.rules))
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
