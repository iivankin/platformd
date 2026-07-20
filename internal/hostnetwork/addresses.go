package hostnetwork

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

type Address struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}

func Addresses() ([]Address, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list host network interfaces: %w", err)
	}
	result := make([]Address, 0)
	for _, current := range interfaces {
		if current.Flags&net.FlagUp == 0 || current.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := current.Addrs()
		if err != nil {
			return nil, fmt.Errorf("list addresses for interface %s: %w", current.Name, err)
		}
		for _, value := range addresses {
			prefix, err := netip.ParsePrefix(value.String())
			if err != nil || !prefix.Addr().Is4() || prefix.Addr().IsLoopback() || prefix.Addr().IsUnspecified() {
				continue
			}
			result = append(result, Address{Interface: current.Name, Address: prefix.Addr().String()})
		}
	}
	slices.SortFunc(result, func(left, right Address) int {
		if order := strings.Compare(left.Interface, right.Interface); order != 0 {
			return order
		}
		return strings.Compare(left.Address, right.Address)
	})
	return result, nil
}

func HasAddress(interfaceName, address string) (bool, error) {
	addresses, err := Addresses()
	if err != nil {
		return false, err
	}
	return slices.Contains(addresses, Address{Interface: interfaceName, Address: address}), nil
}
