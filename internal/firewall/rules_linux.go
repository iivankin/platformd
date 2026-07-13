//go:build linux

package firewall

import (
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const ipv4AddressLength = 4

type compiledRuleset struct {
	table  *nftables.Table
	chains []*nftables.Chain
	rules  []*nftables.Rule
}

func compileRuleset(name string, projects []Project) compiledRuleset {
	table := &nftables.Table{Name: name, Family: nftables.TableFamilyINet}
	priority := nftables.ChainPriorityMangle
	policy := nftables.ChainPolicyAccept
	input := &nftables.Chain{Name: "input", Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookInput, Priority: priority, Policy: &policy}
	forward := &nftables.Chain{Name: "forward", Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookForward, Priority: priority, Policy: &policy}
	postrouting := &nftables.Chain{Name: "postrouting", Table: table, Type: nftables.ChainTypeNAT, Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource, Policy: &policy}

	compiled := compiledRuleset{table: table, chains: []*nftables.Chain{input, forward, postrouting}}
	compiled.rules = append(compiled.rules, rule(table, input, establishedRelated()...))
	for _, project := range projects {
		compiled.rules = append(compiled.rules,
			rule(table, input, append(matchProjectListener(project, unix.IPPROTO_TCP, DNSPort), verdict(expr.VerdictAccept))...),
			rule(table, input, append(matchProjectListener(project, unix.IPPROTO_UDP, DNSPort), verdict(expr.VerdictAccept))...),
		)
		if project.ObjectStoreEnabled {
			compiled.rules = append(compiled.rules,
				rule(table, input, append(matchProjectListener(project, unix.IPPROTO_TCP, ObjectStorePort), verdict(expr.VerdictAccept))...),
			)
		}
		compiled.rules = append(compiled.rules, rule(table, input, append(matchInputInterface(project.Bridge), verdict(expr.VerdictDrop))...))
	}
	for _, project := range projects {
		compiled.rules = append(compiled.rules, rule(table, input, append(matchIPv4Destination(project.Gateway), verdict(expr.VerdictDrop))...))
	}

	compiled.rules = append(compiled.rules, rule(table, forward, establishedRelated()...))
	for _, project := range projects {
		for _, endpoint := range project.BlockedDatabaseEndpoints {
			compiled.rules = append(compiled.rules,
				rule(table, forward, append(matchDatabaseEndpoint(project, endpoint), verdict(expr.VerdictDrop))...),
			)
		}
	}
	for _, project := range projects {
		expressions := append(matchInputInterface(project.Bridge), matchOutputInterface(project.Bridge)...)
		compiled.rules = append(compiled.rules, rule(table, forward, append(expressions, verdict(expr.VerdictAccept))...))
	}
	// Output-interface drops precede broad project egress accepts, so a new
	// connection can never cross from one project bridge into another.
	for _, project := range projects {
		compiled.rules = append(compiled.rules, rule(table, forward, append(matchOutputInterface(project.Bridge), verdict(expr.VerdictDrop))...))
	}
	for _, project := range projects {
		compiled.rules = append(compiled.rules, rule(table, forward, append(matchInputInterface(project.Bridge), verdict(expr.VerdictAccept))...))
	}

	for _, project := range projects {
		expressions := append(matchIPv4SourcePrefix(project.Subnet), matchOutputInterfaceNotEqual(project.Bridge)...)
		compiled.rules = append(compiled.rules, rule(table, postrouting, append(expressions, &expr.Masq{})...))
	}
	return compiled
}

func matchDatabaseEndpoint(project Project, endpoint DatabaseEndpoint) []expr.Any {
	expressions := append(matchInputInterface(project.Bridge), matchIPv4Destination(endpoint.Address)...)
	return append(expressions,
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(endpoint.Port)},
	)
}

func (compiled compiledRuleset) queue(connection *nftables.Conn) {
	connection.AddTable(compiled.table)
	for _, chain := range compiled.chains {
		connection.AddChain(chain)
	}
	for _, currentRule := range compiled.rules {
		connection.AddRule(currentRule)
	}
}

func rule(table *nftables.Table, chain *nftables.Chain, expressions ...expr.Any) *nftables.Rule {
	return &nftables.Rule{Table: table, Chain: chain, Exprs: expressions}
}

func establishedRelated() []expr.Any {
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
			Xor:            binaryutil.NativeEndian.PutUint32(0),
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		verdict(expr.VerdictAccept),
	}
}

func matchProjectListener(project Project, protocol byte, port uint16) []expr.Any {
	expressions := append(matchInputInterface(project.Bridge), matchIPv4Destination(project.Gateway)...)
	return append(expressions,
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
	)
}

func matchInputInterface(name string) []expr.Any {
	return matchInterface(expr.MetaKeyIIFNAME, expr.CmpOpEq, name)
}

func matchOutputInterface(name string) []expr.Any {
	return matchInterface(expr.MetaKeyOIFNAME, expr.CmpOpEq, name)
}

func matchOutputInterfaceNotEqual(name string) []expr.Any {
	return matchInterface(expr.MetaKeyOIFNAME, expr.CmpOpNeq, name)
}

func matchInterface(key expr.MetaKey, operation expr.CmpOp, name string) []expr.Any {
	data := make([]byte, 16)
	copy(data, name)
	return []expr.Any{
		&expr.Meta{Key: key, Register: 1},
		&expr.Cmp{Op: operation, Register: 1, Data: data},
	}
}

func matchIPv4Destination(address netip.Addr) []expr.Any {
	bytes := address.As4()
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: ipv4AddressLength},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: bytes[:]},
	)
}

func matchIPv4SourcePrefix(prefix netip.Prefix) []expr.Any {
	address := prefix.Masked().Addr().As4()
	mask := netipPrefixMask(prefix)
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: ipv4AddressLength},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: ipv4AddressLength, Mask: mask[:], Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: address[:]},
	)
}

func matchIPv4Family() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
	}
}

func netipPrefixMask(prefix netip.Prefix) [4]byte {
	bits := uint32(0xffffffff) << (32 - prefix.Bits())
	return [4]byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)}
}

func verdict(kind expr.VerdictKind) expr.Any {
	return &expr.Verdict{Kind: kind}
}
