//go:build !linux

package projectnetwork

import (
	"fmt"
	"net/netip"
)

func OccupiedPrefixes() ([]netip.Prefix, error) {
	return nil, fmt.Errorf("project network inspection requires Linux")
}

func RemoveBridge(string) error {
	return fmt.Errorf("project network cleanup requires Linux")
}

func MarkBridge(string) error {
	return fmt.Errorf("project network marking requires Linux")
}

func RemoveOwnedBridges() error {
	return fmt.Errorf("project network cleanup requires Linux")
}

func AddVirtualAddress(string, netip.Addr) error {
	return fmt.Errorf("project virtual addresses require Linux")
}

func RemoveVirtualAddress(string, netip.Addr) error {
	return fmt.Errorf("project virtual addresses require Linux")
}
