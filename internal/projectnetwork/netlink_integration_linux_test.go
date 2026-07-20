//go:build linux && integration

package projectnetwork

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestNetlinkInspectionAndExactBridgeCleanup(t *testing.T) {
	if os.Getenv("PLATFORMD_NETWORK_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_NETWORK_INTEGRATION=1 on an isolated root host")
	}
	bridgeName := BridgeName("integration-bridge")
	bridge := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: bridgeName}}
	if err := netlink.LinkAdd(bridge); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if link, err := netlink.LinkByName(bridgeName); err == nil {
			_ = netlink.LinkDel(link)
		}
	})
	if err := netlink.AddrAdd(bridge, &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP("10.80.42.1").To4(), Mask: net.CIDRMask(24, 32)}}); err != nil {
		t.Fatal(err)
	}
	occupied, err := OccupiedPrefixes()
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(occupied, netip.MustParsePrefix("10.80.42.0/24")) {
		t.Fatalf("interface subnet missing from occupied prefixes: %v", occupied)
	}
	virtual := netip.MustParseAddr("10.80.42.192")
	if err := AddVirtualAddress(bridgeName, virtual); err != nil {
		t.Fatal(err)
	}
	if !linkHasAddress(t, bridge, netip.PrefixFrom(virtual, 32)) {
		t.Fatal("project gateway virtual address was not published")
	}
	if err := RemoveVirtualAddress(bridgeName, virtual); err != nil {
		t.Fatal(err)
	}
	if linkHasAddress(t, bridge, netip.PrefixFrom(virtual, 32)) {
		t.Fatal("project gateway virtual address survived removal")
	}
	if err := RemoveBridge(bridgeName); err != nil {
		t.Fatal(err)
	}
	if _, err := netlink.LinkByName(bridgeName); err == nil {
		t.Fatal("project bridge survived exact cleanup")
	}
	markedProjectID := "integration-marked"
	markedName := BridgeName(markedProjectID)
	marked := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: markedName}}
	if err := netlink.LinkAdd(marked); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if link, err := netlink.LinkByName(markedName); err == nil {
			_ = netlink.LinkDel(link)
		}
	})
	if err := MarkBridge(markedProjectID); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOwnedBridges(); err != nil {
		t.Fatal(err)
	}
	if _, err := netlink.LinkByName(markedName); err == nil {
		t.Fatal("marked project bridge survived owned cleanup")
	}

	dummyName := BridgeName("integration-non-bridge")
	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: dummyName}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(dummy) })
	if err := RemoveBridge(dummyName); err == nil {
		t.Fatal("expected non-bridge interface to be preserved")
	}
	if _, err := netlink.LinkByName(dummyName); err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			t.Fatal("non-bridge interface was deleted")
		}
		t.Fatal(err)
	}
}

func linkHasAddress(t *testing.T, link netlink.Link, expected netip.Prefix) bool {
	t.Helper()
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		if address.IPNet == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(address.IPNet.String())
		if err == nil && prefix == expected {
			return true
		}
	}
	return false
}

func containsPrefix(prefixes []netip.Prefix, expected netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix == expected {
			return true
		}
	}
	return false
}
