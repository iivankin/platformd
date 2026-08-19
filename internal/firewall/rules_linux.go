//go:build linux

package firewall

import (
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const (
	ipv4AddressLength     = 4
	ipv4SourceOffset      = 12
	ipv4DestinationOffset = 16
)

type compiledRuleset struct {
	table   *nftables.Table
	chains  []*nftables.Chain
	objects []nftables.Obj
	rules   []*nftables.Rule
}

func compileRuleset(name string, projects []Project) compiledRuleset {
	table := &nftables.Table{Name: name, Family: nftables.TableFamilyINet}
	priority := nftables.ChainPriorityMangle
	policy := nftables.ChainPolicyAccept
	input := &nftables.Chain{Name: "input", Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookInput, Priority: priority, Policy: &policy}
	forward := &nftables.Chain{Name: "forward", Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookForward, Priority: priority, Policy: &policy}
	postrouting := &nftables.Chain{Name: "postrouting", Table: table, Type: nftables.ChainTypeNAT, Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource, Policy: &policy}
	// Locally delivered bridge VIPs skip nat prerouting (nf_nat does not
	// invoke that chain for RTN_LOCAL). Filter prerouting still runs, so
	// TPROXY steals those TCP flows onto the internal tunnel listener.
	filterPre := &nftables.Chain{Name: "filterpre", Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle, Policy: &policy}

	compiled := compiledRuleset{table: table, chains: []*nftables.Chain{input, forward, postrouting, filterPre}}
	compiled.rules = append(compiled.rules, rule(table, input, establishedRelated()...))
	for _, project := range projects {
		// Bridged local delivery shows iif as the veth, not the bridge, so
		// match the gateway address instead of the input interface.
		compiled.rules = append(compiled.rules,
			rule(table, input, matchDestinationPortAccept(project.Gateway, unix.IPPROTO_TCP, DNSPort)...),
			rule(table, input, matchDestinationPortAccept(project.Gateway, unix.IPPROTO_UDP, DNSPort)...),
		)
		if project.RemoteVIP.IsValid() {
			compiled.rules = append(compiled.rules,
				rule(table, filterPre, concatExprs(matchIPv4DestinationPrefix(project.RemoteVIP), matchRemoteVIPTProxy())...),
			)
		}
		if project.ObjectStoreEnabled {
			compiled.rules = append(compiled.rules,
				rule(table, input, matchDestinationPortAccept(project.Gateway, unix.IPPROTO_TCP, ObjectStorePort)...),
			)
		}
		if project.ServiceTelemetryEnabled {
			compiled.rules = append(compiled.rules,
				rule(table, input, matchDestinationPortAccept(project.Gateway, unix.IPPROTO_TCP, ServiceTelemetryPort)...),
				rule(table, input, matchDestinationPortAccept(project.Gateway, unix.IPPROTO_TCP, OTLPHTTPPort)...),
			)
		}
		for _, listener := range project.GatewayListeners {
			protocol := byte(unix.IPPROTO_TCP)
			if listener.Protocol == "udp" {
				protocol = unix.IPPROTO_UDP
			}
			compiled.rules = append(compiled.rules,
				rule(table, input, matchDestinationPortAccept(listener.Address, protocol, listener.Port)...),
			)
		}
		compiled.rules = append(compiled.rules, rule(table, input, append(matchInputInterface(project.Bridge), verdict(expr.VerdictDrop))...))
	}
	for _, project := range projects {
		compiled.rules = append(compiled.rules, rule(table, input, append(matchIPv4Destination(project.Gateway), verdict(expr.VerdictDrop))...))
		for _, listener := range project.GatewayListeners {
			compiled.rules = append(compiled.rules, rule(table, input, append(matchIPv4Destination(listener.Address), verdict(expr.VerdictDrop))...))
		}
	}

	for _, project := range projects {
		for _, endpoint := range project.PublicTrafficEndpoints {
			ingressName := publicCounterName("ingress", endpoint.ServiceID)
			egressName := publicCounterName("egress", endpoint.ServiceID)
			compiled.objects = append(compiled.objects,
				&nftables.CounterObj{Table: table, Name: ingressName},
				&nftables.CounterObj{Table: table, Name: egressName},
			)
			ingress := append(matchOutputInterface(project.Bridge), matchIPv4Destination(endpoint.Address)...)
			ingress = append(ingress, establishedRelatedMatch()...)
			ingress = append(ingress, counterReference(ingressName))
			compiled.rules = append(compiled.rules, rule(table, forward, ingress...))

			egress := append(matchInputInterface(project.Bridge), matchIPv4Source(endpoint.Address)...)
			for _, otherProject := range projects {
				egress = append(egress, matchOutputInterfaceNotEqual(otherProject.Bridge)...)
			}
			egress = append(egress, counterReference(egressName))
			compiled.rules = append(compiled.rules, rule(table, forward, egress...))
		}
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
	return concatExprs(matchInputInterface(project.Bridge), matchIPv4Destination(endpoint.Address), matchTransport(unix.IPPROTO_TCP, endpoint.Port))
}

func (compiled compiledRuleset) queue(connection *nftables.Conn) {
	connection.AddTable(compiled.table)
	for _, chain := range compiled.chains {
		connection.AddChain(chain)
	}
	for _, object := range compiled.objects {
		connection.AddObj(object)
	}
	for _, currentRule := range compiled.rules {
		connection.AddRule(currentRule)
	}
}

func rule(table *nftables.Table, chain *nftables.Chain, expressions ...expr.Any) *nftables.Rule {
	return &nftables.Rule{Table: table, Chain: chain, Exprs: expressions}
}

func establishedRelated() []expr.Any {
	return append(establishedRelatedMatch(), verdict(expr.VerdictAccept))
}

func establishedRelatedMatch() []expr.Any {
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
	}
}

func counterReference(name string) expr.Any {
	return &expr.Objref{Type: int(nftables.ObjTypeCounter), Name: name}
}

func matchDestinationPortAccept(address netip.Addr, protocol byte, port uint16) []expr.Any {
	return concatExprs(matchIPv4Destination(address), matchPortAccept(protocol, port))
}

func matchTransport(protocol byte, port uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
	}
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

func concatExprs(parts ...[]expr.Any) []expr.Any {
	n := 0
	for _, part := range parts {
		n += len(part)
	}
	out := make([]expr.Any, 0, n)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func matchPortAccept(protocol byte, port uint16) []expr.Any {
	return concatExprs(matchTransport(protocol, port), []expr.Any{verdict(expr.VerdictAccept)})
}

func matchRemoteVIPTProxy() []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Immediate{Register: 1, Data: binaryutil.BigEndian.PutUint16(InternalTunnelPort)},
		&expr.TProxy{
			Family:  byte(unix.NFPROTO_IPV4),
			RegPort: 1,
		},
		verdict(expr.VerdictAccept),
	}
}

func matchIPv4DestinationPrefix(prefix netip.Prefix) []expr.Any {
	address := prefix.Masked().Addr().As4()
	mask := netipPrefixMask(prefix)
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: ipv4DestinationOffset, Len: ipv4AddressLength},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: ipv4AddressLength, Mask: mask[:], Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: address[:]},
	)
}

func matchIPv4Destination(address netip.Addr) []expr.Any {
	bytes := address.As4()
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: ipv4DestinationOffset, Len: ipv4AddressLength},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: bytes[:]},
	)
}

func matchIPv4Source(address netip.Addr) []expr.Any {
	bytes := address.As4()
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: ipv4SourceOffset, Len: ipv4AddressLength},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: bytes[:]},
	)
}

func matchIPv4SourcePrefix(prefix netip.Prefix) []expr.Any {
	address := prefix.Masked().Addr().As4()
	mask := netipPrefixMask(prefix)
	return append(matchIPv4Family(),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: ipv4SourceOffset, Len: ipv4AddressLength},
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
