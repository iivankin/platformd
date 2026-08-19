//go:build linux && integration

package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/hostagent"
	"github.com/iivankin/platformd/internal/hosthub"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	hostMeshProjectID       = "hostmesh"
	hostMeshProjectName     = "shop"
	hostMeshParentIP        = "192.0.2.1"
	hostMeshChildIP         = "192.0.2.2"
	hostMeshHubAddr         = "192.0.2.1:9443"
	hostMeshParentPort      = 18080
	hostMeshChildPort       = 18081
	hostMeshParentProbeHost = 10
	hostMeshChildProbeHost  = 13
)

func TestHostInternalMeshTwoInstances(t *testing.T) {
	if os.Getenv("PLATFORMD_HOST_MESH_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_HOST_MESH_INTEGRATION=1 in a delegated systemd unit")
	}
	if os.Getenv("PLATFORMD_HOST_MESH_ROLE") == "child" {
		runHostMeshChild(t)
		return
	}
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" || os.Getenv("PLATFORMD_CGROUP_INTEGRATION") != "1" {
		t.Skip("host mesh integration requires the delegated runtime and cgroup flags")
	}
	if os.Geteuid() != 0 {
		t.Fatal("host mesh integration requires root")
	}
	runHostMeshParent(t)
}

func runHostMeshParent(t *testing.T) {
	t.Helper()
	hostNS := captureHostMeshNamespace(t)
	ipc := hostMeshIPCDir()
	if err := os.RemoveAll(ipc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ipc, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ipc) })

	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	paths := hostMeshLayout("parent")
	resetHostMeshLayout(t, paths)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, master := openHostMeshStore(t, ctx, paths)
	mesh := newInternalBridge("", store.LookupInternalName)
	runtime, err := startHostMeshRuntime(ctx, paths, tree.WorkloadRoot(), []state.RuntimeProject{
		{ID: hostMeshProjectID, Name: hostMeshProjectName},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	project := runtime.firewallProjects[hostMeshProjectID]
	bridgeNS := ensureHostMeshProjectBridge(t, hostNS, project)
	runtime.AttachInternalMesh(mesh)
	if err := mesh.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mesh.Close() })

	hub, err := hosthub.New(hosthub.Config{
		Store: store, Master: master, AdminHostname: hostMeshHubAddr, DialLocal: mesh.DialLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Close)
	mesh.SetHub(hub)

	joinToken, _, err := hub.CreateJoinToken(ctx, "edge-1", "actor", "admin@example.com", "req-token")
	if err != nil {
		t.Fatal(err)
	}

	child := startHostMeshChildProcess(t, ipc, joinToken, tree.WorkloadRoot())
	setupHostMeshLink(t, hostNS, child.Process.Pid)
	parentAddr := netip.MustParseAddr(hostMeshParentIP)
	startHostMeshHTTP(t, net.JoinHostPort(hostMeshParentIP, fmt.Sprintf("%d", hostMeshParentPort)), hostMeshParentBody)
	if err := runtime.dnsZones[hostMeshProjectID].Set("db.shop.internal", parentAddr); err != nil {
		t.Fatal(err)
	}
	if err := runtime.dnsZones[hostMeshProjectID].Set("errors-api.shop.internal", parentAddr); err != nil {
		t.Fatal(err)
	}
	if err := runtime.dnsZones[hostMeshProjectID].Set("otel-api.shop.internal", parentAddr); err != nil {
		t.Fatal(err)
	}
	if err := runtime.dnsZones[hostMeshProjectID].Set("analytics-shop.shop.internal", parentAddr); err != nil {
		t.Fatal(err)
	}
	startHostMeshHub(t, hub)

	waitHostMeshFile(t, ctx, filepath.Join(ipc, "ready"))
	hosts, err := store.Hosts(ctx)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("joined hosts = %v %v", hosts, err)
	}
	if _, err := store.CreateService(ctx, state.CreateService{
		ID: "api", ProjectID: hostMeshProjectID, Name: "api", Enabled: true, HostID: hosts[0].ID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ipc, "catalog"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	probe := attachHostMeshProbe(t, bridgeNS, "pdhmp0", "pdhmp1", project, hostMeshParentProbeHost)
	assertHostMeshName(t, probe, project, "db.shop.internal", parentAddr, false)
	assertHostMeshHTTP(t, probe, "db.shop.internal", hostMeshParentPort, "parent-db")
	assertHostMeshName(t, probe, project, "api.shop.internal", netip.Addr{}, true)
	assertHostMeshHTTP(t, probe, "api.shop.internal", hostMeshChildPort, "child-api")

	waitHostMeshFile(t, ctx, filepath.Join(ipc, "child-ok"))
	if err := os.WriteFile(filepath.Join(ipc, "parent-done"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("child instance: %v", err)
	}
}

func runHostMeshChild(t *testing.T) {
	t.Helper()
	ipc := os.Getenv("PLATFORMD_HOST_MESH_IPC")
	token := os.Getenv("PLATFORMD_HOST_MESH_TOKEN")
	cgroupRoot := os.Getenv("PLATFORMD_HOST_MESH_CGROUP")
	if ipc == "" || token == "" || cgroupRoot == "" {
		t.Fatal("child host mesh environment is incomplete")
	}
	hostNS := captureHostMeshNamespace(t)
	paths := hostMeshLayout("child")
	resetHostMeshLayout(t, paths)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	waitHostMeshTCP(t, ctx, hostMeshHubAddr)
	client := insecureHostMeshClient()
	joined, err := hostagent.Join(ctx, hostagent.JoinInput{
		URL: "https://" + hostMeshHubAddr, Token: token, Name: "edge-1",
		PublicIPv4: "203.0.113.40", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, welcome, err := hostagent.DialWithOptions(ctx, hostagent.DialOptions{
		ParentHostname: hostMeshHubAddr, HostToken: joined.HostToken,
		PublicIPv4: "203.0.113.40", HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() { _ = conn.Serve(ctx) }()

	projects := make([]state.RuntimeProject, 0, len(welcome.Projects))
	for _, project := range welcome.Projects {
		project.ObjectStoreEnabled = false
		projects = append(projects, project)
	}
	runtime, err := startHostMeshRuntime(ctx, paths, cgroupRoot, projects)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	project := runtime.firewallProjects[hostMeshProjectID]
	bridgeNS := ensureHostMeshProjectBridge(t, hostNS, project)
	mesh := newInternalBridge(welcome.HostID, func(lookupCtx context.Context, hostname string) (state.InternalName, error) {
		return hostagent.LookupInternal(lookupCtx, conn, hostname)
	})
	runtime.AttachInternalMesh(mesh)
	if err := mesh.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mesh.Close() })
	tunnel, err := hostagent.DialTunnelWithOptions(ctx, hostagent.TunnelOptions{
		ParentHostname: hostMeshHubAddr, HostToken: joined.HostToken,
		DialLocal: mesh.DialLocal, HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	mesh.SetParent(tunnel)
	go func() { _ = tunnel.Serve(ctx) }()

	childAddr := netip.MustParseAddr(hostMeshChildIP)
	startHostMeshHTTP(t, net.JoinHostPort(hostMeshChildIP, fmt.Sprintf("%d", hostMeshChildPort)), func(*http.Request) string {
		return "child-api"
	})
	if err := runtime.dnsZones[hostMeshProjectID].Set("api.shop.internal", childAddr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ipc, "ready"), []byte(joined.HostID), 0o600); err != nil {
		t.Fatal(err)
	}
	waitHostMeshFile(t, ctx, filepath.Join(ipc, "catalog"))

	probe := attachHostMeshProbe(t, bridgeNS, "pdhmc0", "pdhmc1", project, hostMeshChildProbeHost)
	assertHostMeshName(t, probe, project, "api.shop.internal", childAddr, false)
	assertHostMeshHTTP(t, probe, "api.shop.internal", hostMeshChildPort, "child-api")
	assertHostMeshName(t, probe, project, "db.shop.internal", netip.Addr{}, true)
	assertHostMeshHTTP(t, probe, "db.shop.internal", hostMeshParentPort, "parent-db")
	assertHostMeshName(t, probe, project, "errors-api.shop.internal", netip.Addr{}, true)
	assertHostMeshHTTP(t, probe, "errors-api.shop.internal", hostMeshParentPort, "parent-errors")
	assertHostMeshName(t, probe, project, "otel-api.shop.internal", netip.Addr{}, true)
	assertHostMeshHTTP(t, probe, "otel-api.shop.internal", hostMeshParentPort, "parent-otel")
	assertHostMeshName(t, probe, project, "analytics-shop.shop.internal", netip.Addr{}, true)
	assertHostMeshHTTP(t, probe, "analytics-shop.shop.internal", hostMeshParentPort, "parent-analytics")
	if err := os.WriteFile(filepath.Join(ipc, "child-ok"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitHostMeshFile(t, ctx, filepath.Join(ipc, "parent-done"))
}

func startHostMeshChildProcess(t *testing.T, ipc, token, cgroupRoot string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestHostInternalMeshTwoInstances$", "-test.timeout=3m", "-test.v")
	cmd.Env = hostMeshChildEnv(ipc, token, cgroupRoot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState != nil || cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func setupHostMeshLink(t *testing.T, hostNS netns.NsHandle, childPID int) {
	t.Helper()
	inHostMeshHostNamespace(t, hostNS, func() {
		childNS := waitHostMeshChildNamespace(t, childPID)
		t.Cleanup(func() { _ = childNS.Close() })
		hostLink := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{Name: "pdhm0"},
			PeerName:  "pdhm1",
		}
		if err := netlink.LinkAdd(hostLink); err != nil {
			t.Fatalf("add pdhm0: %v", err)
		}
		t.Cleanup(func() {
			deleteHostMeshLink(hostNS, "pdhm0")
		})
		configured := waitHostMeshLink(t, nil, "pdhm0")
		if err := addHostMeshAddress(nil, configured, hostMeshParentIP+"/30"); err != nil {
			t.Fatalf("address pdhm0: %v", err)
		}
		if err := netlink.LinkSetUp(configured); err != nil {
			t.Fatalf("up pdhm0: %v", err)
		}
		peer := waitHostMeshLink(t, nil, "pdhm1")
		if err := netlink.LinkSetNsFd(peer, int(childNS)); err != nil {
			t.Fatalf("move pdhm1: %v", err)
		}
		childHandle, err := netlink.NewHandleAt(childNS)
		if err != nil {
			t.Fatalf("child netlink: %v", err)
		}
		defer childHandle.Close()
		peer = waitHostMeshLink(t, childHandle, "pdhm1")
		if err := addHostMeshAddress(childHandle, peer, hostMeshChildIP+"/30"); err != nil {
			t.Fatalf("address pdhm1: %v", err)
		}
		if err := childHandle.LinkSetUp(peer); err != nil {
			t.Fatalf("up pdhm1: %v", err)
		}
		loopback := waitHostMeshLink(t, childHandle, "lo")
		if err := childHandle.LinkSetUp(loopback); err != nil {
			t.Fatalf("up lo: %v", err)
		}
	})
}

func startHostMeshHub(t *testing.T, hub *hosthub.Hub) {
	t.Helper()
	listener, err := net.Listen("tcp", hostMeshHubAddr)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(hub.Handler())
	_ = server.Listener.Close()
	server.Listener = listener
	server.EnableHTTP2 = false
	server.TLS = &tls.Config{NextProtos: []string{"http/1.1"}}
	server.StartTLS()
	t.Cleanup(server.Close)
}

func startHostMeshRuntime(ctx context.Context, paths layout.Paths, cgroupRoot string, projects []state.RuntimeProject) (*runtimeStack, error) {
	if err := prepareRuntimeHost(ctx, paths, cgroupRoot); err != nil {
		return nil, err
	}
	stack, err := startRuntime(ctx, paths, cgroupRoot, projects, allowRuntimeGrowth{}, admission.New(), nil)
	if err != nil {
		return nil, err
	}
	if stack.dnsZones[hostMeshProjectID] == nil || stack.firewallProjects[hostMeshProjectID].Bridge == "" {
		_ = stack.Close()
		return nil, fmt.Errorf("shop runtime was not published: zones=%v failures=%v", stack.dnsZones, stack.projectFailures)
	}
	return stack, nil
}

func openHostMeshStore(t *testing.T, ctx context.Context, paths layout.Paths) (*state.Store, cryptobox.MasterKey) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.StateDatabase), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(ctx, paths.StateDatabase, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rawMaster := make([]byte, 32)
	if _, err := rand.Read(rawMaster); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(rawMaster)
	clear(rawMaster)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM, encryptedKey := hostMeshOriginCertificate(t, master)
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "origin-1",
		OriginCertificatePEM: certificatePEM, OriginPrivateKey: encryptedKey,
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: hostMeshProjectID, Name: hostMeshProjectName, AuditEventID: "audit-project",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateObjectStore(ctx, state.CreateObjectStore{
		ID: "db", ProjectID: hostMeshProjectID, Name: "db", BucketName: "shop-db",
		CredentialID: "cred", CredentialName: "root", CredentialPermission: "read_write",
		CredentialSecret: []byte("secret"), CORSOrigins: []string{},
		AuditEventID: "audit-db", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsTracker(ctx, state.AnalyticsTracker{
		ID: "tracker", ProjectID: hostMeshProjectID, Name: "shop", RootDomain: "shop.example",
		Mode: state.AnalyticsModeOptOut, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	return store, master
}

func hostMeshOriginCertificate(t *testing.T, master cryptobox.MasterKey) (string, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "admin.example.com"},
		DNSNames:     []string{"admin.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	encrypted, _, err := origin.EncryptCertificate(master, "origin-1", string(certificatePEM), privatePEM, rand.Reader, 1)
	clear(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted.CertificatePEM, encrypted.PrivateKeyEncrypted
}

func hostMeshLayout(role string) layout.Paths {
	paths := layout.FromRoots(
		"/var/lib/platformd-hostmesh-"+role,
		"/etc/platformd-hostmesh-"+role,
		"/run/platformd-hostmesh-"+role,
		"/tmp/platformd-hostmesh-"+role,
		"/tmp/platformd-hostmesh-"+role+".service",
	)
	return paths
}

func resetHostMeshLayout(t *testing.T, paths layout.Paths) {
	t.Helper()
	for _, root := range []string{paths.DataRoot, paths.ConfigRoot, paths.RuntimeRoot} {
		resetHostMeshRoot(t, root)
	}
	if err := os.MkdirAll(paths.ReleasesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/var/lib/platformd/releases/current", paths.Current); err != nil {
		t.Fatal(err)
	}
}

func resetHostMeshRoot(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	})
}

func hostMeshIPCDir() string {
	if value := os.Getenv("PLATFORMD_HOST_MESH_IPC"); value != "" {
		return value
	}
	return "/run/platformd-hostmesh-ipc"
}

func startHostMeshHTTP(t *testing.T, addr string, body func(*http.Request) string) {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, body(request))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func hostMeshParentBody(request *http.Request) string {
	host := request.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	switch host {
	case "db.shop.internal":
		return "parent-db"
	case "errors-api.shop.internal":
		return "parent-errors"
	case "otel-api.shop.internal":
		return "parent-otel"
	case "analytics-shop.shop.internal":
		return "parent-analytics"
	default:
		return "parent"
	}
}

func insecureHostMeshClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: false,
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
}

type hostMeshProbe struct {
	namespace netns.NsHandle
	gateway   netip.Addr
}

func attachHostMeshProbe(t *testing.T, bridgeNS netns.NsHandle, hostName, peerName string, project firewall.Project, host int) hostMeshProbe {
	t.Helper()
	address, err := projectnetwork.HostAddress(project.Subnet, host)
	if err != nil {
		t.Fatal(err)
	}
	var peerNS netns.NsHandle
	inHostMeshHostNamespace(t, bridgeNS, func() {
		peerNS = createHostMeshPeerNamespace(t, bridgeNS)
		link := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{Name: hostName},
			PeerName:  peerName,
		}
		if err := netlink.LinkAdd(link); err != nil {
			t.Fatalf("add %s: %v", hostName, err)
		}
		t.Cleanup(func() {
			deleteHostMeshLink(bridgeNS, hostName)
		})
		hostLink := waitHostMeshLink(t, nil, hostName)
		peer := waitHostMeshLink(t, nil, peerName)
		if err := netlink.LinkSetNsFd(peer, int(peerNS)); err != nil {
			t.Fatalf("move %s: %v", peerName, err)
		}
		bridge := waitHostMeshLink(t, nil, project.Bridge)
		if err := netlink.LinkSetMaster(hostLink, bridge); err != nil {
			t.Fatalf("enslave %s to %s: %v", hostName, project.Bridge, err)
		}
		if err := netlink.LinkSetUp(hostLink); err != nil {
			t.Fatalf("up %s: %v", hostName, err)
		}
		if err := os.WriteFile("/proc/sys/net/ipv4/conf/"+hostName+"/rp_filter", []byte("2\n"), 0o644); err != nil {
			t.Fatalf("sysctl rp_filter %s: %v", hostName, err)
		}
		configureHostMeshBridge(t, project.Bridge)
		peerHandle, err := netlink.NewHandleAt(peerNS)
		if err != nil {
			t.Fatalf("probe netlink: %v", err)
		}
		defer peerHandle.Close()
		loopback := waitHostMeshLink(t, peerHandle, "lo")
		if err := peerHandle.LinkSetUp(loopback); err != nil {
			t.Fatalf("up probe lo: %v", err)
		}
		peer = waitHostMeshLink(t, peerHandle, peerName)
		if err := addHostMeshAddress(peerHandle, peer, address.String()+"/24"); err != nil {
			t.Fatalf("address %s: %v", peerName, err)
		}
		if err := peerHandle.LinkSetUp(peer); err != nil {
			t.Fatalf("up %s: %v", peerName, err)
		}
		if err := peerHandle.RouteAdd(&netlink.Route{
			LinkIndex: peer.Attrs().Index,
			Gw:        project.Gateway.AsSlice(),
		}); err != nil {
			t.Fatalf("default route via %s: %v", project.Gateway, err)
		}
	})
	return hostMeshProbe{namespace: peerNS, gateway: project.Gateway}
}

func captureHostMeshNamespace(t *testing.T) netns.NsHandle {
	t.Helper()
	runtime.LockOSThread()
	namespace, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	runtime.UnlockOSThread()
	t.Cleanup(func() { _ = namespace.Close() })
	return namespace
}

func ensureHostMeshProjectBridge(t *testing.T, namespace netns.NsHandle, project firewall.Project) netns.NsHandle {
	t.Helper()
	// NetworkCreate only writes the project network config. Netavark creates
	// the host bridge when the first container attaches; this test has no
	// containers, so it creates that bridge and gateway itself.
	inHostMeshHostNamespace(t, namespace, func() {
		if _, err := netlink.LinkByName(project.Bridge); err != nil {
			bridge := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: project.Bridge}}
			if err := netlink.LinkAdd(bridge); err != nil {
				t.Fatalf("add project bridge %s: %v", project.Bridge, err)
			}
		}
		link := waitHostMeshLink(t, nil, project.Bridge)
		parsed, err := netlink.ParseAddr(fmt.Sprintf("%s/%d", project.Gateway, project.Subnet.Bits()))
		if err != nil {
			t.Fatalf("parse project gateway %s: %v", project.Gateway, err)
		}
		if err := netlink.AddrReplace(link, parsed); err != nil {
			t.Fatalf("address project bridge %s: %v", project.Bridge, err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			t.Fatalf("up project bridge %s: %v", project.Bridge, err)
		}
		configureHostMeshBridge(t, project.Bridge)
	})
	t.Cleanup(func() {
		deleteHostMeshLink(namespace, project.Bridge)
	})
	return namespace
}

func configureHostMeshBridge(t *testing.T, name string) {
	t.Helper()
	link := waitHostMeshLink(t, nil, name)
	if err := netlink.LinkSetHardwareAddr(link, hostMeshBridgeMAC()); err != nil {
		t.Fatalf("mac project bridge %s: %v", name, err)
	}
	for _, item := range []struct{ key, value string }{
		{"net/ipv4/conf/" + name + "/rp_filter", "2"},
		{"net/ipv4/conf/" + name + "/forwarding", "1"},
	} {
		if err := os.WriteFile("/proc/sys/"+item.key, []byte(item.value+"\n"), 0o644); err != nil {
			t.Fatalf("sysctl %s: %v", item.key, err)
		}
	}
}

func hostMeshBridgeMAC() net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x42, 0x68, 0x6d, 0x00, 0x01}
}

func inHostMeshHostNamespace(t *testing.T, hostNS netns.NsHandle, action func()) {
	t.Helper()
	runtime.LockOSThread()
	if err := netns.Set(hostNS); err != nil {
		t.Fatalf("restore host network namespace: %v", err)
	}
	defer func() {
		if err := netns.Set(hostNS); err != nil {
			t.Fatalf("restore host network namespace: %v", err)
		}
		runtime.UnlockOSThread()
	}()
	action()
}

func deleteHostMeshLink(hostNS netns.NsHandle, name string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := netns.Set(hostNS); err != nil {
		return
	}
	if link, err := netlink.LinkByName(name); err == nil {
		_ = netlink.LinkDel(link)
	}
}

func createHostMeshPeerNamespace(t *testing.T, hostNS netns.NsHandle) netns.NsHandle {
	t.Helper()
	if err := netns.Set(hostNS); err != nil {
		t.Fatalf("restore host network namespace: %v", err)
	}
	peerNamespace, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := netns.Set(hostNS); err != nil {
		_ = peerNamespace.Close()
		t.Fatalf("restore host network namespace: %v", err)
	}
	t.Cleanup(func() { _ = peerNamespace.Close() })
	return peerNamespace
}

func waitHostMeshChildNamespace(t *testing.T, pid int) netns.NsHandle {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for {
		namespace, err := netns.GetFromPid(pid)
		if err == nil {
			handle, handleErr := netlink.NewHandleAt(namespace)
			if handleErr == nil {
				_, last = handle.LinkByName("lo")
				handle.Close()
				if last == nil {
					return namespace
				}
			} else {
				last = handleErr
			}
			_ = namespace.Close()
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("child netns: %v", last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitHostMeshLink(t *testing.T, handle *netlink.Handle, name string) netlink.Link {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for {
		var link netlink.Link
		var err error
		if handle == nil {
			link, err = netlink.LinkByName(name)
		} else {
			link, err = handle.LinkByName(name)
		}
		if err == nil {
			return link
		}
		last = err
		var notFound netlink.LinkNotFoundError
		if !errors.As(err, &notFound) || time.Now().After(deadline) {
			t.Fatalf("link %s: %v", name, last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hostMeshChildEnv(ipc, token, cgroupRoot string) []string {
	overrides := map[string]string{
		"PLATFORMD_HOST_MESH_INTEGRATION": "1",
		"PLATFORMD_HOST_MESH_ROLE":        "child",
		"PLATFORMD_HOST_MESH_IPC":         ipc,
		"PLATFORMD_HOST_MESH_TOKEN":       token,
		"PLATFORMD_HOST_MESH_CGROUP":      cgroupRoot,
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, found := overrides[key]; found {
			continue
		}
		env = append(env, item)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func addHostMeshAddress(handle *netlink.Handle, link netlink.Link, cidr string) error {
	parsed, err := netlink.ParseAddr(cidr)
	if err != nil {
		return err
	}
	if handle == nil {
		return netlink.AddrAdd(link, parsed)
	}
	return handle.AddrAdd(link, parsed)
}

func lookupHostMeshIPv4(ctx context.Context, probe hostMeshProbe, name string) (netip.Addr, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "udp4", net.JoinHostPort(probe.gateway.String(), "53"))
		},
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip4", name)
	if err != nil || len(addresses) != 1 {
		return netip.Addr{}, fmt.Errorf("lookup %s: %v %v", name, addresses, err)
	}
	return addresses[0], nil
}

func assertHostMeshName(t *testing.T, probe hostMeshProbe, project firewall.Project, name string, local netip.Addr, remote bool) {
	t.Helper()
	var resolved netip.Addr
	inHostMeshNamespace(t, probe.namespace, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		address, err := lookupHostMeshIPv4(ctx, probe, name)
		if err != nil {
			return err
		}
		resolved = address
		return nil
	})
	if remote {
		vip, err := projectnetwork.RemoteVIPPrefix(project.Subnet)
		if err != nil || !vip.Contains(resolved) {
			t.Fatalf("%s remote VIP = %s subnet=%s", name, resolved, project.Subnet)
		}
		return
	}
	if resolved != local {
		t.Fatalf("%s = %s, want local %s", name, resolved, local)
	}
}

func assertHostMeshHTTP(t *testing.T, probe hostMeshProbe, name string, port int, body string) {
	t.Helper()
	inHostMeshNamespace(t, probe.namespace, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		address, err := lookupHostMeshIPv4(ctx, probe, name)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/", address, port), nil)
		if err != nil {
			return err
		}
		request.Host = name
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			return fmt.Errorf("GET %s: %w", name, err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 64))
		if err != nil {
			return err
		}
		if string(payload) != body {
			return fmt.Errorf("GET %s = %q, want %q", name, payload, body)
		}
		return nil
	})
}

func inHostMeshNamespace(t *testing.T, namespace netns.NsHandle, action func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		originNS, err := netns.Get()
		if err != nil {
			runtime.UnlockOSThread()
			done <- err
			return
		}
		if err := netns.Set(namespace); err != nil {
			_ = originNS.Close()
			runtime.UnlockOSThread()
			done <- err
			return
		}
		err = action()
		restoreErr := netns.Set(originNS)
		_ = originNS.Close()
		runtime.UnlockOSThread()
		done <- errors.Join(err, restoreErr)
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitHostMeshFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", path, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitHostMeshTCP(t *testing.T, ctx context.Context, addr string) {
	t.Helper()
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", addr, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
