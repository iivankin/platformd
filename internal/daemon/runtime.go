package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
)

type runtimeStack struct {
	mu               sync.Mutex
	ctx              context.Context
	closed           bool
	engine           *containerengine.Engine
	firewall         *firewall.Manager
	forwarder        *internaldns.ForwardCache
	upstreams        []netip.AddrPort
	firewallProjects map[string]firewall.Project
	networks         []string
	projectFailures  []projectnetwork.Failure
	dnsServers       []*internaldns.Server
	dnsZones         map[string]*internaldns.Zone
	projectNetworks  map[string]containerengine.Network
	paths            layout.Paths
	cgroupRoot       string
	deployments      *deployment.Controller
	serviceFailures  map[string]error
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
	var upstreams []netip.AddrPort
	if len(projectPlan.Assignments) > 0 {
		var readErr error
		upstreams, readErr = internaldns.ReadUpstreams("/etc/resolv.conf")
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
		ctx: ctx, engine: engine, firewall: manager, forwarder: forwarder,
		upstreams:        slices.Clone(upstreams),
		firewallProjects: make(map[string]firewall.Project),
		projectFailures:  append(cleanupFailures, projectPlan.Failures...),
		dnsZones:         make(map[string]*internaldns.Zone),
		projectNetworks:  make(map[string]containerengine.Network),
		paths:            paths,
		cgroupRoot:       cgroupWorkloadRoot,
		serviceFailures:  make(map[string]error),
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
		stack.firewallProjects[assignment.ProjectID] = firewallProjects[len(firewallProjects)-1]
	}
	if err := manager.Apply(firewallProjects); err != nil {
		return nil, errors.Join(err, stack.Close())
	}
	return stack, nil
}

func (stack *runtimeStack) Close() error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return nil
	}
	stack.closed = true
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

func (stack *runtimeStack) AddProject(project state.RuntimeProject) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return errors.New("container runtime is closed")
	}
	if _, exists := stack.projectNetworks[project.ID]; exists {
		return nil
	}
	if err := projectnetwork.RemoveBridge(projectnetwork.BridgeName(project.ID)); err != nil {
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	occupied, err := projectnetwork.OccupiedPrefixes()
	if err != nil {
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	for _, current := range stack.firewallProjects {
		occupied = append(occupied, current.Subnet)
	}
	plan, err := projectnetwork.Plan([]projectnetwork.Project{{ID: project.ID, Name: project.Name}}, occupied)
	if err != nil {
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	if len(plan.Failures) != 0 {
		stack.projectFailures = append(stack.projectFailures, plan.Failures...)
		return plan.Failures[0].Err
	}
	if len(plan.Assignments) != 1 {
		return errors.New("project network planner returned no assignment")
	}
	assignment := plan.Assignments[0]
	if err := stack.ensureForwarder(assignment.Gateway); err != nil {
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	network, err := stack.engine.CreateNetwork(containerengine.NetworkSpec{
		Name: assignment.NetworkName, Interface: assignment.Bridge,
		Subnet: assignment.Subnet.String(), Gateway: assignment.Gateway.String(),
		Labels: map[string]string{
			"io.platformd.owner": "project", "io.platformd.project-id": project.ID,
		},
	})
	if err != nil {
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	if network.Interface != assignment.Bridge || network.Subnet != assignment.Subnet.String() || network.Gateway != assignment.Gateway.String() {
		_ = stack.engine.RemoveNetwork(network.Name)
		err := fmt.Errorf("network inspect differs from requested topology: %+v", network)
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	zone, err := internaldns.NewZone(nil)
	if err != nil {
		_ = stack.engine.RemoveNetwork(network.Name)
		return err
	}
	view, err := internaldns.NewView(zone, stack.forwarder)
	if err != nil {
		_ = stack.engine.RemoveNetwork(network.Name)
		return err
	}
	dnsServer, err := internaldns.Start(stack.ctx, internaldns.ServerConfig{
		Address: assignment.Gateway, Port: firewall.DNSPort, FreeBind: true, View: view,
	})
	if err != nil {
		_ = stack.engine.RemoveNetwork(network.Name)
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	firewallProject := firewall.Project{
		ID: project.ID, Bridge: network.Interface, Subnet: assignment.Subnet,
		Gateway: assignment.Gateway, ObjectStoreEnabled: project.ObjectStoreEnabled,
	}
	candidate := make([]firewall.Project, 0, len(stack.firewallProjects)+1)
	for _, current := range stack.firewallProjects {
		candidate = append(candidate, current)
	}
	candidate = append(candidate, firewallProject)
	if err := stack.firewall.Apply(candidate); err != nil {
		_ = dnsServer.Close()
		_ = stack.engine.RemoveNetwork(network.Name)
		stack.recordProjectFailure(project.ID, err)
		return err
	}
	stack.networks = append(stack.networks, network.Name)
	stack.dnsServers = append(stack.dnsServers, dnsServer)
	stack.dnsZones[project.ID] = zone
	stack.projectNetworks[project.ID] = network
	stack.firewallProjects[project.ID] = firewallProject
	return nil
}

func (stack *runtimeStack) ensureForwarder(gateway netip.Addr) error {
	for _, upstream := range stack.upstreams {
		if upstream.Addr().Unmap() == gateway {
			return fmt.Errorf("upstream DNS address %s conflicts with project gateway", gateway)
		}
	}
	if stack.forwarder != nil {
		return nil
	}
	upstreams, err := internaldns.ReadUpstreams("/etc/resolv.conf")
	if err != nil {
		return err
	}
	forwarder, err := internaldns.NewForwardCache(upstreams, []netip.Addr{gateway})
	if err != nil {
		return err
	}
	stack.upstreams = upstreams
	stack.forwarder = forwarder
	return nil
}

func (stack *runtimeStack) recordProjectFailure(projectID string, err error) {
	stack.projectFailures = append(stack.projectFailures, projectnetwork.Failure{ProjectID: projectID, Err: err})
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
