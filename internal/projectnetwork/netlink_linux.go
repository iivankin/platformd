//go:build linux

package projectnetwork

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
)

const bridgeAliasPrefix = "platformd:project:"

func OccupiedPrefixes() ([]netip.Prefix, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("list IPv4 routes: %w", err)
	}
	addresses, err := netlink.AddrList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("list IPv4 interface addresses: %w", err)
	}
	result := make([]netip.Prefix, 0, len(routes)+len(addresses))
	for _, route := range routes {
		if route.Dst == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Dst.String())
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("parse IPv4 route %q", route.Dst)
		}
		if prefix.Bits() != 0 {
			result = append(result, prefix.Masked())
		}
	}
	for _, address := range addresses {
		if address.IPNet == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(address.IPNet.String())
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("parse IPv4 interface address %q", address.IPNet)
		}
		result = append(result, prefix.Masked())
	}
	return result, nil
}

func RemoveBridge(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("inspect stale project bridge %s: %w", name, err)
	}
	if link.Type() != "bridge" {
		return fmt.Errorf("refusing to remove non-bridge interface %s of type %s", name, link.Type())
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("remove stale project bridge %s: %w", name, err)
	}
	return nil
}

func MarkBridge(projectID string) error {
	name := BridgeName(projectID)
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("inspect project bridge %s: %w", name, err)
	}
	if link.Type() != "bridge" {
		return fmt.Errorf("project interface %s has unexpected type %s", name, link.Type())
	}
	if err := netlink.LinkSetAlias(link, bridgeAliasPrefix+projectID); err != nil {
		return fmt.Errorf("mark project bridge %s: %w", name, err)
	}
	return nil
}

func RemoveOwnedBridges() error {
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("list interfaces for stale project bridges: %w", err)
	}
	for _, link := range links {
		alias := link.Attrs().Alias
		projectID, marked := strings.CutPrefix(alias, bridgeAliasPrefix)
		if !marked || projectID == "" || link.Type() != "bridge" || link.Attrs().Name != BridgeName(projectID) {
			continue
		}
		if err := netlink.LinkDel(link); err != nil {
			return fmt.Errorf("remove owned stale project bridge %s: %w", link.Attrs().Name, err)
		}
	}
	return nil
}

func AddVirtualAddress(interfaceName string, address netip.Addr) error {
	if !address.IsValid() || !address.Is4() || address.IsUnspecified() {
		return fmt.Errorf("invalid project virtual address %s", address)
	}
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return fmt.Errorf("inspect project interface %s: %w", interfaceName, err)
	}
	prefix := &net.IPNet{IP: net.IP(address.AsSlice()), Mask: net.CIDRMask(32, 32)}
	if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: prefix}); err != nil {
		return fmt.Errorf("add project virtual address %s to %s: %w", address, interfaceName, err)
	}
	return nil
}

func RemoveVirtualAddress(interfaceName string, address netip.Addr) error {
	if !address.IsValid() || !address.Is4() {
		return nil
	}
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("inspect project interface %s: %w", interfaceName, err)
	}
	prefix := &net.IPNet{IP: net.IP(address.AsSlice()), Mask: net.CIDRMask(32, 32)}
	if err := netlink.AddrDel(link, &netlink.Addr{IPNet: prefix}); err != nil && !errors.Is(err, syscall.EADDRNOTAVAIL) {
		return fmt.Errorf("remove project virtual address %s from %s: %w", address, interfaceName, err)
	}
	return nil
}
