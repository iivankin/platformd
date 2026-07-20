//go:build linux && amd64 && cgo

package containerengine

import (
	"fmt"
	"net"

	nettypes "go.podman.io/common/libnetwork/types"
)

func (e *Engine) CreateNetwork(spec NetworkSpec) (Network, error) {
	if spec.Name == "" || spec.Interface == "" {
		return Network{}, fmt.Errorf("network name and interface are required")
	}
	subnet, err := nettypes.ParseCIDR(spec.Subnet)
	if err != nil {
		return Network{}, fmt.Errorf("parse network subnet: %w", err)
	}
	gateway := net.ParseIP(spec.Gateway)
	if gateway == nil || gateway.To4() == nil || !subnet.Contains(gateway) {
		return Network{}, fmt.Errorf("gateway %q is not an IPv4 address inside %s", spec.Gateway, spec.Subnet)
	}
	var leaseRange *nettypes.LeaseRange
	if spec.LeaseStart != "" || spec.LeaseEnd != "" {
		start := net.ParseIP(spec.LeaseStart)
		end := net.ParseIP(spec.LeaseEnd)
		if start == nil || start.To4() == nil || end == nil || end.To4() == nil ||
			!subnet.Contains(start) || !subnet.Contains(end) {
			return Network{}, fmt.Errorf("lease range %q..%q is outside %s", spec.LeaseStart, spec.LeaseEnd, spec.Subnet)
		}
		leaseRange = &nettypes.LeaseRange{StartIP: start.To4(), EndIP: end.To4()}
	}

	created, err := e.runtime.Network().NetworkCreate(nettypes.Network{
		Name:             spec.Name,
		Driver:           "bridge",
		NetworkInterface: spec.Interface,
		Subnets: []nettypes.Subnet{{
			Subnet:     subnet,
			Gateway:    gateway,
			LeaseRange: leaseRange,
		}},
		IPv6Enabled: false,
		DNSEnabled:  false,
		Labels:      cloneStrings(spec.Labels),
		Options:     map[string]string{"isolate": "true"},
	}, &nettypes.NetworkCreateOptions{})
	if err != nil {
		return Network{}, fmt.Errorf("create network %s: %w", spec.Name, err)
	}
	return publicNetwork(created), nil
}

func (e *Engine) InspectNetwork(name string) (Network, error) {
	network, err := e.runtime.Network().NetworkInspect(name)
	if err != nil {
		return Network{}, fmt.Errorf("inspect network %s: %w", name, err)
	}
	return publicNetwork(network), nil
}

func (e *Engine) RemoveNetwork(name string) error {
	if err := e.runtime.Network().NetworkRemove(name); err != nil {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

func publicNetwork(network nettypes.Network) Network {
	result := Network{
		ID:        network.ID,
		Name:      network.Name,
		Interface: network.NetworkInterface,
	}
	if len(network.Subnets) == 1 {
		result.Subnet = network.Subnets[0].Subnet.String()
		result.Gateway = network.Subnets[0].Gateway.String()
	}
	return result
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
