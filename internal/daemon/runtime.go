package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
)

type runtimeStack struct {
	engine          *containerengine.Engine
	firewall        *firewall.Manager
	networks        []string
	projectFailures []projectnetwork.Failure
	dnsServers      []*internaldns.Server
	dnsZones        map[string]*internaldns.Zone
	projectNetworks map[string]containerengine.Network
}

func startRuntime(ctx context.Context, paths layout.Paths, cgroupWorkloadRoot string, projects []state.RuntimeProject) (*runtimeStack, error) {
	manager := firewall.New()
	if err := manager.Clear(); err != nil {
		return nil, fmt.Errorf("clear previous platform firewall: %w", err)
	}
	if err := firewall.EnableIPv4Forwarding(); err != nil {
		return nil, err
	}
	for _, directory := range []string{paths.GeneratedRoot, paths.BackupWorkRoot} {
		if err := resetTransientDirectory(directory); err != nil {
			return nil, err
		}
	}
	if err := projectnetwork.RemoveOwnedBridges(); err != nil {
		return nil, err
	}
	projectInputs := make([]projectnetwork.Project, 0, len(projects))
	var cleanupFailures []projectnetwork.Failure
	for _, project := range projects {
		if err := projectnetwork.RemoveBridge(projectnetwork.BridgeName(project.ID)); err != nil {
			cleanupFailures = append(cleanupFailures, projectnetwork.Failure{ProjectID: project.ID, Err: err})
			continue
		}
		projectInputs = append(projectInputs, projectnetwork.Project{ID: project.ID, Name: project.Name})
	}
	occupied, err := projectnetwork.OccupiedPrefixes()
	if err != nil {
		return nil, err
	}
	projectPlan, err := projectnetwork.Plan(projectInputs, occupied)
	if err != nil {
		return nil, err
	}
	disallowedResolvers := make([]netip.Addr, 0, len(projectPlan.Assignments))
	for _, assignment := range projectPlan.Assignments {
		disallowedResolvers = append(disallowedResolvers, assignment.Gateway)
	}
	var forwarder *internaldns.ForwardCache
	if len(projectPlan.Assignments) > 0 {
		upstreams, readErr := internaldns.ReadUpstreams("/etc/resolv.conf")
		if readErr != nil {
			return nil, readErr
		}
		forwarder, err = internaldns.NewForwardCache(upstreams, disallowedResolvers)
		if err != nil {
			return nil, err
		}
	}

	config := containerengine.ProductionConfig(paths, cgroupWorkloadRoot)
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		return nil, fmt.Errorf("prepare private container storage: %w", err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		return nil, errors.Join(err, manager.Clear())
	}
	stack := &runtimeStack{
		engine: engine, firewall: manager,
		projectFailures: append(cleanupFailures, projectPlan.Failures...),
		dnsZones:        make(map[string]*internaldns.Zone),
		projectNetworks: make(map[string]containerengine.Network),
	}
	objectStores := make(map[string]bool, len(projects))
	for _, project := range projects {
		objectStores[project.ID] = project.ObjectStoreEnabled
	}
	var firewallProjects []firewall.Project
	for _, assignment := range projectPlan.Assignments {
		network, createErr := engine.CreateNetwork(containerengine.NetworkSpec{
			Name: assignment.NetworkName, Interface: assignment.Bridge,
			Subnet: assignment.Subnet.String(), Gateway: assignment.Gateway.String(),
			Labels: map[string]string{
				"io.platformd.owner":      "project",
				"io.platformd.project-id": assignment.ProjectID,
			},
		})
		if createErr != nil {
			stack.projectFailures = append(stack.projectFailures, projectnetwork.Failure{ProjectID: assignment.ProjectID, Err: createErr})
			continue
		}
		if network.Interface != assignment.Bridge || network.Subnet != assignment.Subnet.String() || network.Gateway != assignment.Gateway.String() {
			_ = engine.RemoveNetwork(network.Name)
			stack.projectFailures = append(stack.projectFailures, projectnetwork.Failure{
				ProjectID: assignment.ProjectID,
				Err:       fmt.Errorf("network inspect differs from requested topology: %+v", network),
			})
			continue
		}
		zone, zoneErr := internaldns.NewZone(nil)
		if zoneErr != nil {
			_ = engine.RemoveNetwork(network.Name)
			return nil, errors.Join(zoneErr, stack.Close())
		}
		view, viewErr := internaldns.NewView(zone, forwarder)
		if viewErr != nil {
			_ = engine.RemoveNetwork(network.Name)
			return nil, errors.Join(viewErr, stack.Close())
		}
		dnsServer, dnsErr := internaldns.Start(ctx, internaldns.ServerConfig{
			Address: assignment.Gateway, Port: firewall.DNSPort, FreeBind: true, View: view,
		})
		if dnsErr != nil {
			_ = engine.RemoveNetwork(network.Name)
			stack.projectFailures = append(stack.projectFailures, projectnetwork.Failure{ProjectID: assignment.ProjectID, Err: dnsErr})
			continue
		}
		stack.networks = append(stack.networks, network.Name)
		stack.dnsServers = append(stack.dnsServers, dnsServer)
		stack.dnsZones[assignment.ProjectID] = zone
		stack.projectNetworks[assignment.ProjectID] = network
		firewallProjects = append(firewallProjects, firewall.Project{
			ID: assignment.ProjectID, Bridge: network.Interface,
			Subnet: assignment.Subnet, Gateway: assignment.Gateway,
			ObjectStoreEnabled: objectStores[assignment.ProjectID],
		})
	}
	if err := manager.Apply(firewallProjects); err != nil {
		return nil, errors.Join(err, stack.Close())
	}
	return stack, nil
}

func (stack *runtimeStack) Close() error {
	var failures []error
	for index := len(stack.dnsServers) - 1; index >= 0; index-- {
		failures = append(failures, stack.dnsServers[index].Close())
	}
	for index := len(stack.networks) - 1; index >= 0; index-- {
		failures = append(failures, stack.engine.RemoveNetwork(stack.networks[index]))
	}
	failures = append(failures, stack.engine.Close(), stack.firewall.Clear())
	return errors.Join(failures...)
}

func resetTransientDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return fmt.Errorf("unsafe transient directory %q", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clear transient directory %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create transient directory %s: %w", path, err)
	}
	return nil
}
