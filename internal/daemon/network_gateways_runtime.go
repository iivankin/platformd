package daemon

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/hostnetwork"
	"github.com/iivankin/platformd/internal/portproxy"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
)

const networkGatewayRoutePrefix = "network-gateway:"

type networkGatewayPublication struct {
	gateway  effectiveNetworkGateway
	address  netip.Addr
	hostname string
}

func (stack *runtimeStack) EnableNetworkGateway(effective effectiveNetworkGateway, proxy *portproxy.Manager) error {
	if proxy == nil {
		return errors.New("network gateway proxy is unavailable")
	}
	gateway := effective.gateway
	if effective.namespacePID == 0 {
		addressPresent, err := hostnetwork.HasAddress(gateway.InterfaceName, gateway.SourceAddress)
		if err != nil {
			return err
		}
		if !addressPresent {
			return fmt.Errorf("%s no longer owns %s", gateway.InterfaceName, gateway.SourceAddress)
		}
	} else if effective.namespacePID < 1 {
		return errors.New("managed private network namespace is unavailable")
	}

	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return errors.New("container runtime is closed")
	}
	if current, exists := stack.networkGatewayPublications[gateway.ID]; exists {
		if err := stack.disableNetworkGatewayLocked(current, proxy); err != nil {
			return err
		}
	}
	publication := networkGatewayPublication{gateway: effective}
	if gateway.Mode == "import" {
		if err := stack.enableImportedNetworkGatewayLocked(&publication, proxy); err != nil {
			stack.networkGatewayFailures[gateway.ID] = err
			return err
		}
	} else {
		if err := proxy.Add(exportNetworkGatewayRoute(effective)); err != nil {
			stack.networkGatewayFailures[gateway.ID] = err
			return err
		}
	}
	stack.networkGatewayPublications[gateway.ID] = publication
	delete(stack.networkGatewayFailures, gateway.ID)
	return nil
}

func (stack *runtimeStack) enableImportedNetworkGatewayLocked(publication *networkGatewayPublication, proxy *portproxy.Manager) error {
	gateway := publication.gateway
	stored := gateway.gateway
	network, networkExists := stack.projectNetworks[stored.ProjectID]
	zone := stack.dnsZones[stored.ProjectID]
	project, projectExists := stack.firewallProjects[stored.ProjectID]
	if !networkExists || zone == nil || !projectExists {
		return fmt.Errorf("project %s network runtime is unavailable", stored.ProjectID)
	}
	subnet, err := netip.ParsePrefix(network.Subnet)
	if err != nil {
		return fmt.Errorf("parse project subnet: %w", err)
	}
	address, err := projectnetwork.HostAddress(subnet, stored.InternalSlot)
	if err != nil {
		return err
	}
	publication.address = address
	publication.hostname = stored.Name + "." + stored.ProjectName + ".internal"
	if err := projectnetwork.AddVirtualAddress(network.Interface, address); err != nil {
		return err
	}
	if err := proxy.Add(importNetworkGatewayRoute(gateway, address)); err != nil {
		_ = projectnetwork.RemoveVirtualAddress(network.Interface, address)
		return err
	}
	listener := firewall.GatewayListener{
		Address: address, Protocol: stored.Protocol, Port: uint16(stored.ListenPort),
	}
	project.GatewayListeners = append(project.GatewayListeners, listener)
	if err := stack.applyFirewallProjectLocked(project); err != nil {
		_ = proxy.Remove(networkGatewayRouteID(stored.ID))
		_ = projectnetwork.RemoveVirtualAddress(network.Interface, address)
		return err
	}
	stack.firewallProjects[stored.ProjectID] = project
	if err := zone.Set(publication.hostname, address); err != nil {
		project.GatewayListeners = removeGatewayListener(project.GatewayListeners, listener)
		_ = stack.applyFirewallProjectLocked(project)
		stack.firewallProjects[stored.ProjectID] = project
		_ = proxy.Remove(networkGatewayRouteID(stored.ID))
		_ = projectnetwork.RemoveVirtualAddress(network.Interface, address)
		return err
	}
	return nil
}

func (stack *runtimeStack) DisableNetworkGateway(gateway state.NetworkGateway, proxy *portproxy.Manager) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	publication, exists := stack.networkGatewayPublications[gateway.ID]
	if !exists {
		delete(stack.networkGatewayFailures, gateway.ID)
		return proxy.Remove(networkGatewayRouteID(gateway.ID))
	}
	err := stack.disableNetworkGatewayLocked(publication, proxy)
	delete(stack.networkGatewayFailures, gateway.ID)
	return err
}

func (stack *runtimeStack) disableNetworkGatewayLocked(publication networkGatewayPublication, proxy *portproxy.Manager) error {
	stored := publication.gateway.gateway
	delete(stack.networkGatewayPublications, stored.ID)
	failures := []error{proxy.Remove(networkGatewayRouteID(stored.ID))}
	if stored.Mode != "import" {
		return errors.Join(failures...)
	}
	zone := stack.dnsZones[stored.ProjectID]
	if zone != nil && publication.hostname != "" {
		failures = append(failures, zone.Delete(publication.hostname))
	}
	project, exists := stack.firewallProjects[stored.ProjectID]
	if exists {
		listener := firewall.GatewayListener{
			Address: publication.address, Protocol: stored.Protocol,
			Port: uint16(stored.ListenPort),
		}
		project.GatewayListeners = removeGatewayListener(project.GatewayListeners, listener)
		if err := stack.applyFirewallProjectLocked(project); err != nil {
			failures = append(failures, err)
		} else {
			stack.firewallProjects[stored.ProjectID] = project
		}
	}
	if network, exists := stack.projectNetworks[stored.ProjectID]; exists {
		failures = append(failures, projectnetwork.RemoveVirtualAddress(network.Interface, publication.address))
	}
	return errors.Join(failures...)
}

func (stack *runtimeStack) NetworkGatewayStatus(gatewayID string) (string, string) {
	stack.mu.Lock()
	failure := stack.networkGatewayFailures[gatewayID]
	_, running := stack.networkGatewayPublications[gatewayID]
	closed := stack.closed
	stack.mu.Unlock()
	if failure != nil {
		return "failed", failure.Error()
	}
	if closed || !running {
		return "pending", "Network gateway is not published"
	}
	return "running", ""
}

func (stack *runtimeStack) recordNetworkGatewayFailure(gatewayID string, err error) {
	stack.mu.Lock()
	stack.networkGatewayFailures[gatewayID] = err
	stack.mu.Unlock()
}

func importNetworkGatewayRoute(effective effectiveNetworkGateway, address netip.Addr) portproxy.Route {
	gateway := effective.gateway
	return portproxy.Route{
		ID: networkGatewayRouteID(gateway.ID), Protocol: gateway.Protocol,
		ListenAddress: address.String(), ListenPort: gateway.ListenPort,
		Target: portproxy.AddressTarget{
			Host: gateway.RemoteHost, Port: gateway.RemotePort, SourceAddress: gateway.SourceAddress,
		},
		DialNamespacePID: effective.namespacePID,
	}
}

func exportNetworkGatewayRoute(effective effectiveNetworkGateway) portproxy.Route {
	gateway := effective.gateway
	return portproxy.Route{
		ID: networkGatewayRouteID(gateway.ID), Protocol: gateway.Protocol,
		ListenAddress: gateway.SourceAddress, ListenPort: gateway.ListenPort,
		Target:             portproxy.ServiceTarget{ServiceID: gateway.TargetServiceID, Port: gateway.TargetPort},
		ListenNamespacePID: effective.namespacePID,
	}
}

func networkGatewayRouteID(gatewayID string) string {
	return networkGatewayRoutePrefix + gatewayID
}

func removeGatewayListener(listeners []firewall.GatewayListener, target firewall.GatewayListener) []firewall.GatewayListener {
	index := slices.Index(listeners, target)
	if index < 0 {
		return listeners
	}
	return slices.Delete(slices.Clone(listeners), index, index+1)
}
