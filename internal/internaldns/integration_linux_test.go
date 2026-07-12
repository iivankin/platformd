//go:build linux && integration

package internaldns

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/net/dns/dnsmessage"
)

func TestFreeBoundListenerAnswersAfterGatewayAppears(t *testing.T) {
	if os.Getenv("PLATFORMD_DNS_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_DNS_INTEGRATION=1 on an isolated root host")
	}
	const interfaceName = "pddnsit0"
	if existing, err := netlink.LinkByName(interfaceName); err == nil {
		if err := netlink.LinkDel(existing); err != nil {
			t.Fatal(err)
		}
	}
	address := netip.MustParseAddr("10.80.254.1")
	view := mustView(t, map[string]netip.Addr{"api.alpha.internal": netip.MustParseAddr("10.80.254.2")}, forwarderFunc(func(context.Context, []byte) ([]byte, error) {
		t.Fatal("internal query was forwarded")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := Start(ctx, ServerConfig{Address: address, Port: 1053, FreeBind: true, View: view})
	if err != nil {
		t.Fatalf("pre-bind absent gateway: %v", err)
	}
	defer server.Close()

	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(dummy) })
	if err := netlink.AddrAdd(dummy, &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(address.String()).To4(), Mask: net.CIDRMask(24, 32)}}); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(dummy); err != nil {
		t.Fatal(err)
	}

	connection, err := net.DialTimeout("udp4", netip.AddrPortFrom(address, server.Port()).String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(dnsQuery(t, 30, "api.alpha.internal.", dnsmessage.TypeA)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, maxDNSMessageBytes)
	length, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	assertDNSResult(t, buffer[:length], dnsmessage.RCodeSuccess, "10.80.254.2")
}
