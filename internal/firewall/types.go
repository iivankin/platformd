package firewall

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

const (
	TableName       = "platformd"
	DNSPort         = 53
	ObjectStorePort = 9000
)

// Project describes only inspected runtime network facts. It is deliberately
// free of product state so the entire firewall can be recreated after a crash.
type Project struct {
	ID                       string
	Bridge                   string
	Subnet                   netip.Prefix
	Gateway                  netip.Addr
	ObjectStoreEnabled       bool
	BlockedDatabaseEndpoints []DatabaseEndpoint
	GatewayListeners         []GatewayListener
}

type GatewayListener struct {
	Address  netip.Addr
	Protocol string
	Port     uint16
}

// DatabaseEndpoint is process-local maintenance state compiled into the same
// authoritative ruleset as project isolation. It is never persisted.
type DatabaseEndpoint struct {
	Address netip.Addr
	Port    uint16
}

func canonicalProjects(projects []Project) ([]Project, error) {
	result := slices.Clone(projects)
	slices.SortFunc(result, func(left, right Project) int {
		return strings.Compare(left.ID, right.ID)
	})
	for index := range result {
		project := &result[index]
		project.Subnet = project.Subnet.Masked()
		project.BlockedDatabaseEndpoints = slices.Clone(project.BlockedDatabaseEndpoints)
		slices.SortFunc(project.BlockedDatabaseEndpoints, func(left, right DatabaseEndpoint) int {
			if order := left.Address.Compare(right.Address); order != 0 {
				return order
			}
			return int(left.Port) - int(right.Port)
		})
		project.GatewayListeners = slices.Clone(project.GatewayListeners)
		slices.SortFunc(project.GatewayListeners, func(left, right GatewayListener) int {
			if order := left.Address.Compare(right.Address); order != 0 {
				return order
			}
			if order := strings.Compare(left.Protocol, right.Protocol); order != 0 {
				return order
			}
			return int(left.Port) - int(right.Port)
		})
		if err := validateProject(*project); err != nil {
			return nil, err
		}
		for previousIndex := range index {
			previous := result[previousIndex]
			if project.ID == previous.ID {
				return nil, fmt.Errorf("duplicate firewall project ID %q", project.ID)
			}
			if project.Bridge == previous.Bridge {
				return nil, fmt.Errorf("duplicate firewall bridge %q", project.Bridge)
			}
			if project.Gateway == previous.Gateway {
				return nil, fmt.Errorf("duplicate firewall gateway %s", project.Gateway)
			}
			if project.Subnet.Contains(previous.Subnet.Addr()) || previous.Subnet.Contains(project.Subnet.Addr()) {
				return nil, fmt.Errorf("overlapping firewall subnets %s and %s", previous.Subnet, project.Subnet)
			}
		}
	}
	return result, nil
}

func validateProject(project Project) error {
	if project.ID == "" {
		return fmt.Errorf("firewall project ID is empty")
	}
	if project.Bridge == "" || len(project.Bridge) > 15 || strings.ContainsRune(project.Bridge, 0) {
		return fmt.Errorf("firewall project %q has invalid bridge %q", project.ID, project.Bridge)
	}
	if !project.Subnet.IsValid() || !project.Subnet.Addr().Is4() {
		return fmt.Errorf("firewall project %q requires an IPv4 subnet", project.ID)
	}
	if !project.Gateway.IsValid() || !project.Gateway.Is4() || !project.Subnet.Contains(project.Gateway) {
		return fmt.Errorf("firewall project %q gateway %s is outside %s", project.ID, project.Gateway, project.Subnet)
	}
	if project.Gateway == project.Subnet.Addr() || project.Gateway == lastAddress(project.Subnet) {
		return fmt.Errorf("firewall project %q gateway %s is not a usable host address", project.ID, project.Gateway)
	}
	for index, endpoint := range project.BlockedDatabaseEndpoints {
		if !endpoint.Address.IsValid() || !endpoint.Address.Is4() || !project.Subnet.Contains(endpoint.Address) ||
			endpoint.Address == project.Gateway || endpoint.Port == 0 {
			return fmt.Errorf("firewall project %q has invalid blocked database endpoint %s:%d", project.ID, endpoint.Address, endpoint.Port)
		}
		if index > 0 && endpoint == project.BlockedDatabaseEndpoints[index-1] {
			return fmt.Errorf("firewall project %q has duplicate blocked database endpoint %s:%d", project.ID, endpoint.Address, endpoint.Port)
		}
	}
	for index, listener := range project.GatewayListeners {
		if !listener.Address.IsValid() || !listener.Address.Is4() || !project.Subnet.Contains(listener.Address) ||
			listener.Address == project.Gateway || listener.Port == 0 ||
			(listener.Protocol != "tcp" && listener.Protocol != "udp") {
			return fmt.Errorf("firewall project %q has invalid gateway listener %+v", project.ID, listener)
		}
		if index > 0 && listener == project.GatewayListeners[index-1] {
			return fmt.Errorf("firewall project %q has duplicate gateway listener %+v", project.ID, listener)
		}
	}
	return nil
}

func lastAddress(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Masked().Addr().As4()
	hostBits := 32 - prefix.Bits()
	value := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	value |= uint32(1<<hostBits) - 1
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}
