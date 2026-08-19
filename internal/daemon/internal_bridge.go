package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/hosttunnel"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
)

type internalNameLookup func(context.Context, string) (state.InternalName, error)

type tunnelHub interface {
	Tunnel(hostID string) *hosttunnel.Peer
}

type bridgedProject struct {
	zone   *internaldns.Zone
	iface  string
	subnet netip.Prefix
	vip    netip.Prefix
	byName map[string]netip.Addr
	byAddr map[netip.Addr]string
}

type internalBridge struct {
	selfHostID string
	lookup     internalNameLookup
	addAddress func(string, netip.Addr) error

	mu       sync.Mutex
	hub      tunnelHub
	parent   *hosttunnel.Peer
	projects map[string]*bridgedProject
	listener net.Listener
}

func newInternalBridge(selfHostID string, lookup internalNameLookup) *internalBridge {
	return &internalBridge{
		selfHostID: selfHostID,
		lookup:     lookup,
		addAddress: projectnetwork.AddVirtualAddress,
		projects:   map[string]*bridgedProject{},
	}
}

func (bridge *internalBridge) SetHub(hub tunnelHub) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.hub = hub
}

func (bridge *internalBridge) SetParent(peer *hosttunnel.Peer) {
	bridge.mu.Lock()
	previous := bridge.parent
	bridge.parent = peer
	bridge.mu.Unlock()
	if previous != nil && previous != peer {
		_ = previous.Close()
	}
}

func (bridge *internalBridge) RegisterProject(projectID string, zone *internaldns.Zone, project firewall.Project) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.projects[projectID] = &bridgedProject{
		zone: zone, iface: project.Bridge, subnet: project.Subnet, vip: project.RemoteVIP,
		byName: map[string]netip.Addr{}, byAddr: map[netip.Addr]string{},
	}
}

func (bridge *internalBridge) UnregisterProject(projectID string) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	delete(bridge.projects, projectID)
}

func (bridge *internalBridge) Resolver(projectID string) internaldns.RemoteResolver {
	return projectRemoteResolver{bridge: bridge, projectID: projectID}
}

type projectRemoteResolver struct {
	bridge    *internalBridge
	projectID string
}

func (resolver projectRemoteResolver) ResolveRemote(ctx context.Context, hostname string) (netip.Addr, bool, error) {
	return resolver.bridge.resolveRemote(ctx, resolver.projectID, hostname)
}

func (bridge *internalBridge) resolveRemote(ctx context.Context, projectID, hostname string) (netip.Addr, bool, error) {
	hostname = canonicalInternalHostname(hostname)
	if hostname == "" || bridge.lookup == nil {
		return netip.Addr{}, false, nil
	}
	if _, ok := bridge.lookupLocal(hostname); ok {
		return netip.Addr{}, false, nil
	}
	owned, err := bridge.lookup(ctx, hostname)
	if err != nil {
		return netip.Addr{}, false, err
	}
	if !owned.Found {
		return netip.Addr{}, false, nil
	}
	if owned.HostID == bridge.selfHostID {
		return netip.Addr{}, false, nil
	}
	return bridge.allocateVIP(projectID, hostname)
}

func (bridge *internalBridge) allocateVIP(projectID, hostname string) (netip.Addr, bool, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	project := bridge.projects[projectID]
	if project == nil || !project.vip.IsValid() {
		return netip.Addr{}, false, errors.New("project has no remote .internal VIP range")
	}
	if address, ok := project.byName[hostname]; ok {
		return address, true, nil
	}
	for host := projectnetwork.RemoteVIPFirstHost; host <= projectnetwork.RemoteVIPLastHost; host++ {
		address, err := projectnetwork.HostAddress(project.subnet, host)
		if err != nil {
			return netip.Addr{}, false, err
		}
		if _, used := project.byAddr[address]; used {
			continue
		}
		if err := bridge.addAddress(project.iface, address); err != nil {
			return netip.Addr{}, false, err
		}
		project.byName[hostname] = address
		project.byAddr[address] = hostname
		return address, true, nil
	}
	return netip.Addr{}, false, errors.New("remote .internal VIP pool is exhausted")
}

func (bridge *internalBridge) DialLocal(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
	if port == 0 {
		return nil, errors.New("internal tunnel port is required")
	}
	hostname = canonicalInternalHostname(hostname)
	if address, ok := bridge.lookupLocal(hostname); ok {
		if bridge.isRemoteVIP(address) {
			return nil, fmt.Errorf("refusing to dial remote VIP %s for %s", address, hostname)
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), fmt.Sprintf("%d", port)))
	}
	if bridge.selfHostID != "" {
		return nil, fmt.Errorf("local .internal name %s is not published on this child", hostname)
	}
	if bridge.lookup == nil {
		return nil, fmt.Errorf("unknown .internal name %s", hostname)
	}
	owned, err := bridge.lookup(ctx, hostname)
	if err != nil {
		return nil, err
	}
	if !owned.Found || owned.HostID == "" {
		return nil, fmt.Errorf("local .internal name %s is not published yet", hostname)
	}
	bridge.mu.Lock()
	hub := bridge.hub
	bridge.mu.Unlock()
	if hub == nil {
		return nil, errors.New("child server hub is not configured")
	}
	peer := hub.Tunnel(owned.HostID)
	if peer == nil {
		return nil, errors.New("child server is offline")
	}
	return peer.Dial(ctx, hostname, port)
}

func (bridge *internalBridge) lookupLocal(hostname string) (netip.Addr, bool) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for _, project := range bridge.projects {
		if project.zone == nil {
			continue
		}
		address, ok := project.zone.Lookup(hostname)
		if ok {
			return address, true
		}
	}
	return netip.Addr{}, false
}

func (bridge *internalBridge) hostnameForVIP(address netip.Addr) string {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for _, project := range bridge.projects {
		if hostname, ok := project.byAddr[address]; ok {
			return hostname
		}
	}
	return ""
}

func (bridge *internalBridge) isRemoteVIP(address netip.Addr) bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for _, project := range bridge.projects {
		if project.vip.IsValid() && project.vip.Contains(address) {
			return true
		}
	}
	return false
}

func (bridge *internalBridge) Listen(ctx context.Context) error {
	listener, err := listenInternalTunnel(ctx)
	if err != nil {
		return err
	}
	bridge.mu.Lock()
	bridge.listener = listener
	bridge.mu.Unlock()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go bridge.handleAccepted(ctx, conn)
		}
	}()
	return nil
}

func (bridge *internalBridge) Close() error {
	bridge.mu.Lock()
	listener := bridge.listener
	parent := bridge.parent
	bridge.listener = nil
	bridge.parent = nil
	bridge.mu.Unlock()
	var failures []error
	if listener != nil {
		failures = append(failures, listener.Close())
	}
	if parent != nil {
		failures = append(failures, parent.Close())
	}
	return errors.Join(failures...)
}

func (bridge *internalBridge) handleAccepted(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	destination, err := netip.ParseAddrPort(conn.LocalAddr().String())
	if err != nil {
		log.Printf("internal tunnel local destination: %v", err)
		return
	}
	destination = netip.AddrPortFrom(destination.Addr().Unmap(), destination.Port())
	hostname := bridge.hostnameForVIP(destination.Addr())
	if hostname == "" {
		log.Printf("internal tunnel unknown VIP %s", destination)
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	backend, err := bridge.dialAccepted(dialCtx, hostname, destination.Port())
	cancel()
	if err != nil {
		log.Printf("internal tunnel dial %s:%d: %v", hostname, destination.Port(), err)
		return
	}
	spliceConnections(conn, backend)
}

func (bridge *internalBridge) dialAccepted(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
	if bridge.selfHostID == "" {
		return bridge.DialLocal(ctx, hostname, port)
	}
	bridge.mu.Lock()
	parent := bridge.parent
	bridge.mu.Unlock()
	if parent == nil {
		return nil, errors.New("parent .internal tunnel is not connected")
	}
	return parent.Dial(ctx, hostname, port)
}

func spliceConnections(left, right net.Conn) {
	defer left.Close()
	defer right.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(left, right)
		close(done)
	}()
	_, _ = io.Copy(right, left)
	<-done
}

func canonicalInternalHostname(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func listenInternalTunnel(ctx context.Context) (net.Listener, error) {
	config := net.ListenConfig{Control: configureInternalTunnelSocket}
	listener, err := config.Listen(ctx, "tcp4", fmt.Sprintf(":%d", firewall.InternalTunnelPort))
	if err != nil {
		return nil, fmt.Errorf("listen for .internal tunnel: %w", err)
	}
	return listener, nil
}
