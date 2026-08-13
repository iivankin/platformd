package daemon

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/iivankin/platformd/internal/firewall"
)

func (stack *runtimeStack) ServiceTelemetryGateway(projectID string) (netip.Addr, error) {
	stack.mu.Lock()
	network, exists := stack.projectNetworks[projectID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || !exists {
		return netip.Addr{}, errors.New("project network is unavailable")
	}
	address, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse project gateway: %w", err)
	}
	return address, nil
}

func (stack *runtimeStack) PublishServiceTelemetry(projectID, hostname string) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return errors.New("container runtime is closed")
	}
	network, networkExists := stack.projectNetworks[projectID]
	zone := stack.dnsZones[projectID]
	project, projectExists := stack.firewallProjects[projectID]
	if !networkExists || zone == nil || !projectExists {
		return fmt.Errorf("project %s network runtime is unavailable", projectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return fmt.Errorf("parse project gateway: %w", err)
	}
	if err := zone.Set(hostname, gateway); err != nil {
		return err
	}
	if project.ServiceTelemetryEnabled {
		return nil
	}
	project.ServiceTelemetryEnabled = true
	candidate := make([]firewall.Project, 0, len(stack.firewallProjects))
	for currentID, current := range stack.firewallProjects {
		if currentID == projectID {
			current = project
		}
		candidate = append(candidate, current)
	}
	if err := stack.firewall.Apply(candidate); err != nil {
		_ = zone.Delete(hostname)
		return err
	}
	stack.firewallProjects[projectID] = project
	return nil
}

func (stack *runtimeStack) UnpublishServiceTelemetry(projectID, hostname string) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return errors.New("container runtime is closed")
	}
	zone := stack.dnsZones[projectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", projectID)
	}
	return zone.Delete(hostname)
}
