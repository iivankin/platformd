package daemon

import (
	"context"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/hosttunnel"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
)

func TestInternalBridgeAllocatesStableRemoteVIP(t *testing.T) {
	t.Parallel()

	zone, err := internaldns.NewZone(nil)
	if err != nil {
		t.Fatal(err)
	}
	var added []netip.Addr
	bridge := newInternalBridge("child-1", func(context.Context, string) (state.InternalName, error) {
		return state.InternalName{Found: true, ProjectID: "shop"}, nil
	})
	bridge.addAddress = func(_ string, address netip.Addr) error {
		added = append(added, address)
		return nil
	}
	bridge.RegisterProject("shop", zone, firewall.Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
		Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
	})

	first, ok, err := bridge.resolveRemote(context.Background(), "shop", "db.shop.internal")
	if err != nil || !ok || first != netip.MustParseAddr("10.80.1.160") {
		t.Fatalf("first VIP = %s ok=%t err=%v", first, ok, err)
	}
	second, ok, err := bridge.resolveRemote(context.Background(), "shop", "db.shop.internal")
	if err != nil || !ok || second != first || len(added) != 1 {
		t.Fatalf("stable VIP = %s added=%v err=%v", second, added, err)
	}
}

func TestInternalBridgeSkipsNamesOwnedHere(t *testing.T) {
	t.Parallel()

	zone, err := internaldns.NewZone(map[string]netip.Addr{
		"api.shop.internal": netip.MustParseAddr("10.80.1.4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge := newInternalBridge("child-1", func(context.Context, string) (state.InternalName, error) {
		t.Fatal("lookup must not run for a local zone hit")
		return state.InternalName{}, nil
	})
	bridge.RegisterProject("shop", zone, firewall.Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
		Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
	})
	if _, ok, err := bridge.resolveRemote(context.Background(), "shop", "api.shop.internal"); err != nil || ok {
		t.Fatalf("local name remote resolve ok=%t err=%v", ok, err)
	}

	bridge.lookup = func(context.Context, string) (state.InternalName, error) {
		return state.InternalName{Found: true, ProjectID: "shop", HostID: "child-1"}, nil
	}
	if _, ok, err := bridge.resolveRemote(context.Background(), "shop", "other.shop.internal"); err != nil || ok {
		t.Fatalf("self-owned name remote resolve ok=%t err=%v", ok, err)
	}

	primary := newInternalBridge("", func(context.Context, string) (state.InternalName, error) {
		return state.InternalName{Found: true, ProjectID: "shop"}, nil
	})
	primary.RegisterProject("shop", zone, firewall.Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
		Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
	})
	if _, ok, err := primary.resolveRemote(context.Background(), "shop", "db.shop.internal"); err != nil || ok {
		t.Fatalf("primary-owned name remote resolve ok=%t err=%v", ok, err)
	}
}

func TestInternalBridgeDialsLocalZoneOnly(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	zone, err := internaldns.NewZone(map[string]netip.Addr{
		"db.shop.internal": netip.MustParseAddr("127.0.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge := newInternalBridge("child-1", nil)
	bridge.RegisterProject("shop", zone, firewall.Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
		Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := bridge.DialLocal(ctx, "db.shop.internal", port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	backend := <-accepted
	defer backend.Close()
	buffer := make([]byte, 2)
	if _, err := io.ReadFull(backend, buffer); err != nil || string(buffer) != "ok" {
		t.Fatalf("backend read = %q %v", buffer, err)
	}
	if _, err := bridge.DialLocal(ctx, "api.shop.internal", port); err == nil {
		t.Fatal("child DialLocal reached a name that is not local")
	}
}

func TestInternalBridgeVIPHostRange(t *testing.T) {
	t.Parallel()
	first, err := projectnetwork.HostAddress(netip.MustParsePrefix("10.80.1.0/24"), projectnetwork.RemoteVIPFirstHost)
	if err != nil {
		t.Fatal(err)
	}
	if first.String() != "10.80.1.160" {
		t.Fatalf("first remote VIP = %s", first)
	}
}

func TestInternalBridgeRefusesDialingRemoteVIP(t *testing.T) {
	t.Parallel()

	zone, err := internaldns.NewZone(map[string]netip.Addr{
		"ghost.shop.internal": netip.MustParseAddr("10.80.1.160"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, selfHostID := range []string{"", "child-1"} {
		bridge := newInternalBridge(selfHostID, func(context.Context, string) (state.InternalName, error) {
			t.Fatal("catalog lookup must not run after a VIP zone hit")
			return state.InternalName{}, nil
		})
		bridge.RegisterProject("shop", zone, firewall.Project{
			ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
			Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
		})
		_, err := bridge.DialLocal(context.Background(), "ghost.shop.internal", 8080)
		if err == nil || !strings.Contains(err.Error(), "refusing to dial remote VIP") {
			t.Fatalf("selfHostID=%q DialLocal VIP = %v", selfHostID, err)
		}
	}
}

func TestInternalBridgeVIPUsesQueryingProjectSubnet(t *testing.T) {
	t.Parallel()

	empty, err := internaldns.NewZone(nil)
	if err != nil {
		t.Fatal(err)
	}
	bridge := newInternalBridge("child-1", func(_ context.Context, hostname string) (state.InternalName, error) {
		if hostname != "db.other.internal" {
			t.Fatalf("lookup %s", hostname)
		}
		return state.InternalName{Found: true, ProjectID: "other", HostID: "child-2"}, nil
	})
	bridge.addAddress = func(string, netip.Addr) error { return nil }
	bridge.RegisterProject("shop", empty, firewall.Project{
		ID: "shop", Bridge: "pd-shop", Subnet: netip.MustParsePrefix("10.80.1.0/24"),
		Gateway: netip.MustParseAddr("10.80.1.1"), RemoteVIP: netip.MustParsePrefix("10.80.1.160/27"),
	})
	bridge.RegisterProject("other", empty, firewall.Project{
		ID: "other", Bridge: "pd-other", Subnet: netip.MustParsePrefix("10.80.2.0/24"),
		Gateway: netip.MustParseAddr("10.80.2.1"), RemoteVIP: netip.MustParsePrefix("10.80.2.160/27"),
	})

	shopVIP, ok, err := bridge.resolveRemote(context.Background(), "shop", "db.other.internal")
	if err != nil || !ok || shopVIP != netip.MustParseAddr("10.80.1.160") {
		t.Fatalf("shop VIP = %s ok=%t err=%v", shopVIP, ok, err)
	}
	otherVIP, ok, err := bridge.resolveRemote(context.Background(), "other", "db.other.internal")
	if err != nil || !ok || otherVIP != netip.MustParseAddr("10.80.2.160") {
		t.Fatalf("owner VIP = %s ok=%t err=%v", otherVIP, ok, err)
	}
}

func TestInternalBridgeParentDialsChildTunnel(t *testing.T) {
	t.Parallel()

	listener := startBridgeEcho(t, "child-b")
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	client, _ := startBridgeTunnelPair(t, func(_ context.Context, hostname string, got uint16) (net.Conn, error) {
		if hostname != "web.shop.internal" || got != port {
			t.Fatalf("tunneled %s:%d", hostname, got)
		}
		return net.Dial("tcp", listener.Addr().String())
	})
	bridge := newInternalBridge("", func(_ context.Context, hostname string) (state.InternalName, error) {
		if hostname != "web.shop.internal" {
			return state.InternalName{}, nil
		}
		return state.InternalName{Found: true, ProjectID: "shop", HostID: "child-b"}, nil
	})
	bridge.SetHub(stubTunnelHub{peers: map[string]*hosttunnel.Peer{"child-b": client}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := bridge.DialLocal(ctx, "web.shop.internal", port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := readBridgeEcho(t, conn); got != "child-b" {
		t.Fatalf("parent→child = %q", got)
	}
}

func TestInternalBridgeParentReportsOfflineChild(t *testing.T) {
	t.Parallel()

	bridge := newInternalBridge("", func(context.Context, string) (state.InternalName, error) {
		return state.InternalName{Found: true, ProjectID: "shop", HostID: "child-b"}, nil
	})
	_, err := bridge.DialLocal(context.Background(), "web.shop.internal", 8080)
	if err == nil || !strings.Contains(err.Error(), "child server hub is not configured") {
		t.Fatalf("missing hub = %v", err)
	}
	bridge.SetHub(stubTunnelHub{})
	_, err = bridge.DialLocal(context.Background(), "web.shop.internal", 8080)
	if err == nil || !strings.Contains(err.Error(), "child server is offline") {
		t.Fatalf("offline child = %v", err)
	}
}

func TestInternalBridgeTwoChildrenRelayThroughParent(t *testing.T) {
	t.Parallel()

	apiListener := startBridgeEcho(t, "child-a-api")
	webListener := startBridgeEcho(t, "child-b-web")
	apiPort := uint16(apiListener.Addr().(*net.TCPAddr).Port)
	webPort := uint16(webListener.Addr().(*net.TCPAddr).Port)
	apiClient, _ := startBridgeTunnelPair(t, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "api.shop.internal" || port != apiPort {
			t.Fatalf("child-a tunneled %s:%d", hostname, port)
		}
		return net.Dial("tcp", apiListener.Addr().String())
	})
	webClient, _ := startBridgeTunnelPair(t, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "web.shop.internal" || port != webPort {
			t.Fatalf("child-b tunneled %s:%d", hostname, port)
		}
		return net.Dial("tcp", webListener.Addr().String())
	})

	parent := newInternalBridge("", func(_ context.Context, hostname string) (state.InternalName, error) {
		switch hostname {
		case "api.shop.internal":
			return state.InternalName{Found: true, ProjectID: "shop", HostID: "child-a"}, nil
		case "web.shop.internal":
			return state.InternalName{Found: true, ProjectID: "shop", HostID: "child-b"}, nil
		default:
			return state.InternalName{}, nil
		}
	})
	parent.SetHub(stubTunnelHub{peers: map[string]*hosttunnel.Peer{
		"child-a": apiClient, "child-b": webClient,
	}})

	childA := newInternalBridge("child-a", nil)
	parentForA, _ := startBridgeTunnelPair(t, parent.DialLocal)
	childA.SetParent(parentForA)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fromA, err := childA.dialAccepted(ctx, "web.shop.internal", webPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fromA.Close()
	if got := readBridgeEcho(t, fromA); got != "child-b-web" {
		t.Fatalf("child-a→child-b = %q", got)
	}

	fromParent, err := parent.DialLocal(ctx, "api.shop.internal", apiPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fromParent.Close()
	if got := readBridgeEcho(t, fromParent); got != "child-a-api" {
		t.Fatalf("parent→child-a = %q", got)
	}
}

type stubTunnelHub struct {
	peers map[string]*hosttunnel.Peer
}

func (hub stubTunnelHub) Tunnel(hostID string) *hosttunnel.Peer {
	return hub.peers[hostID]
}

type testFrameConn struct {
	incoming <-chan []byte
	outgoing chan<- []byte
	done     <-chan struct{}
}

func (conn testFrameConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-conn.done:
		return nil, net.ErrClosed
	case payload := <-conn.incoming:
		return payload, nil
	}
}

func (conn testFrameConn) Write(_ context.Context, payload []byte) error {
	select {
	case <-conn.done:
		return net.ErrClosed
	case conn.outgoing <- append([]byte(nil), payload...):
		return nil
	}
}

func (conn testFrameConn) Close() error { return nil }

func startBridgeTunnelPair(t *testing.T, dial hosttunnel.DialLocal) (*hosttunnel.Peer, *hosttunnel.Peer) {
	t.Helper()
	left := make(chan []byte, 16)
	right := make(chan []byte, 16)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	client := hosttunnel.NewPeer(testFrameConn{incoming: right, outgoing: left, done: done}, nil)
	server := hosttunnel.NewServerPeer(testFrameConn{incoming: left, outgoing: right, done: done}, dial)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = client.Serve(ctx) }()
	go func() { _ = server.Serve(ctx) }()
	return client, server
}

func startBridgeEcho(t *testing.T, body string) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buffer := make([]byte, 1)
				if _, err := io.ReadFull(conn, buffer); err != nil {
					return
				}
				_, _ = io.WriteString(conn, body)
				_, _ = io.Copy(io.Discard, conn)
			}(conn)
		}
	}()
	return listener
}

func readBridgeEcho(t *testing.T, conn net.Conn) string {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("?")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, err := io.ReadAtLeast(conn, buffer, 1)
	if err != nil && n == 0 {
		t.Fatal(err)
	}
	return string(buffer[:n])
}
