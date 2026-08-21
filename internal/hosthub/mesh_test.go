package hosthub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/hostagent"
	"github.com/iivankin/platformd/internal/hosttunnel"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestHostMeshTwoInstances(t *testing.T) {
	env := startHostMesh(t)
	joinToken, _, err := env.hub.CreateJoinToken(context.Background(), "edge-1", "actor", "admin@example.com", "req-token")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("tunnel without control is conflict", func(t *testing.T) {
		joined, err := hostagent.Join(context.Background(), hostagent.JoinInput{
			URL: env.server.URL, Token: joinToken, Name: "edge-1",
			PublicIPv4: "203.0.113.40", HTTPClient: env.client,
		})
		if err != nil {
			t.Fatal(err)
		}
		env.hostID = joined.HostID
		env.hostToken = joined.HostToken
		_, err = hostagent.DialTunnelWithOptions(context.Background(), hostagent.TunnelOptions{
			ParentURL: env.server.URL, HostToken: joined.HostToken, HTTPClient: env.client,
		})
		if err == nil || !strings.Contains(err.Error(), "409") {
			t.Fatalf("tunnel before control = %v", err)
		}
	})
	if env.hostToken == "" {
		t.Fatal("join did not produce a host token")
	}

	if _, err := env.store.CreateService(context.Background(), state.CreateService{
		ID: "api", ProjectID: "shop", Name: "api", Enabled: true, HostID: env.hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-api", CreatedAtMillis: 30,
	}); err != nil {
		t.Fatal(err)
	}

	parentListener := startEcho(t, "parent-db")
	childListener := startEcho(t, "child-api")
	parentPort := uint16(parentListener.Addr().(*net.TCPAddr).Port)
	childPort := uint16(childListener.Addr().(*net.TCPAddr).Port)

	reconciled := make(chan string, 1)
	withdrawn := make(chan string, 1)
	synced := make(chan string, 1)
	var conn *hostagent.Conn
	conn, welcome, err := hostagent.DialWithOptions(context.Background(), hostagent.DialOptions{
		ParentURL: env.server.URL, HostToken: env.hostToken, PublicIPv4: "203.0.113.40",
		HTTPClient: env.client,
		Handlers: hostagent.Handlers{
			Reconcile: func(serviceID string, _ bool) error {
				if _, err := hostagent.NewRemoteStore(conn).DesiredService(context.Background(), serviceID); err != nil {
					return err
				}
				reconciled <- serviceID
				return nil
			},
			Withdraw: func(serviceID string) error {
				withdrawn <- serviceID
				return nil
			},
			SyncPublic: func(serviceID string) error {
				synced <- serviceID
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if welcome.HostID != env.hostID || welcome.ParentHostname != env.hostname || len(welcome.Certificates) != 1 {
		t.Fatalf("welcome = %+v", welcome)
	}
	if len(welcome.AssignedServiceIDs) != 1 || welcome.AssignedServiceIDs[0] != "api" {
		t.Fatalf("assigned = %v", welcome.AssignedServiceIDs)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	t.Cleanup(stopServe)
	go func() { _ = conn.Serve(serveCtx) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Connected(env.hostID) })

	t.Run("lookup internal rpc", func(t *testing.T) {
		child, err := hostagent.LookupInternal(context.Background(), conn, "api.shop.internal")
		if err != nil || !child.Found || child.HostID != env.hostID {
			t.Fatalf("api = %+v %v", child, err)
		}
		primary, err := hostagent.LookupInternal(context.Background(), conn, "db.shop.internal")
		if err != nil || !primary.Found || primary.HostID != "" {
			t.Fatalf("db = %+v %v", primary, err)
		}
		errorsName, err := hostagent.LookupInternal(context.Background(), conn, "errors-api.shop.internal")
		if err != nil || !errorsName.Found || errorsName.HostID != "" {
			t.Fatalf("errors-api = %+v %v", errorsName, err)
		}
		otelName, err := hostagent.LookupInternal(context.Background(), conn, "otel-api.shop.internal")
		if err != nil || !otelName.Found || otelName.HostID != "" {
			t.Fatalf("otel-api = %+v %v", otelName, err)
		}
		analyticsName, err := hostagent.LookupInternal(context.Background(), conn, "analytics-shop.shop.internal")
		if err != nil || !analyticsName.Found || analyticsName.HostID != "" {
			t.Fatalf("analytics-shop = %+v %v", analyticsName, err)
		}
		missing, err := hostagent.LookupInternal(context.Background(), conn, "missing.shop.internal")
		if err != nil || missing.Found {
			t.Fatalf("missing = %+v %v", missing, err)
		}
	})

	t.Run("service listeners rpc and sync-public", func(t *testing.T) {
		if _, err := env.store.AttachServiceListener(context.Background(), state.AttachServiceListenerInput{
			ProjectID: "shop", ServiceID: "api", Protocol: "tcp", PublicPort: 2222, TargetPort: 22,
			AuditEventID: "audit-listener", ActorKind: "access", ActorID: "actor",
			ActorEmail: "admin@example.com", CreatedAtMillis: 40,
		}); err != nil {
			t.Fatal(err)
		}
		listeners, err := hostagent.ServiceListeners(context.Background(), conn, "shop", "api")
		if err != nil || len(listeners) != 1 || listeners[0].PublicPort != 2222 || listeners[0].TargetPort != 22 {
			t.Fatalf("listeners = %+v %v", listeners, err)
		}
		if err := env.hub.SyncPublic(env.hostID, "api"); err != nil {
			t.Fatal(err)
		}
		select {
		case serviceID := <-synced:
			if serviceID != "api" {
				t.Fatalf("sync-public = %s", serviceID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("sync-public was not delivered")
		}
	})

	t.Run("reconcile and withdraw", func(t *testing.T) {
		if err := env.hub.Reconcile(context.Background(), env.hostID, "api", false); err != nil {
			t.Fatal(err)
		}
		select {
		case serviceID := <-reconciled:
			if serviceID != "api" {
				t.Fatalf("reconcile = %s", serviceID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reconcile was not delivered")
		}
		if err := env.hub.Withdraw(context.Background(), env.hostID, "api"); err != nil {
			t.Fatal(err)
		}
		select {
		case serviceID := <-withdrawn:
			if serviceID != "api" {
				t.Fatalf("withdraw = %s", serviceID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("withdraw was not delivered")
		}
	})

	tunnel, err := hostagent.DialTunnelWithOptions(context.Background(), hostagent.TunnelOptions{
		ParentURL: env.server.URL, HostToken: env.hostToken, HTTPClient: env.client,
		DialLocal: func(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
			if hostname != "api.shop.internal" || port != childPort {
				return nil, errTestDial
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", childListener.Addr().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
	go func() { _ = tunnel.Serve(context.Background()) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Tunnel(env.hostID) != nil })

	t.Run("child reaches parent over tunnel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := tunnel.Dial(ctx, "db.shop.internal", parentPort)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got := readEcho(t, conn); got != "parent-db" {
			t.Fatalf("child→parent = %q", got)
		}
	})

	t.Run("parent reaches child over tunnel", func(t *testing.T) {
		peer := env.hub.Tunnel(env.hostID)
		if peer == nil {
			t.Fatal("parent tunnel peer is missing")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := peer.Dial(ctx, "api.shop.internal", childPort)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got := readEcho(t, conn); got != "child-api" {
			t.Fatalf("parent→child = %q", got)
		}
	})

	t.Run("tunnel reconnect replaces peer", func(t *testing.T) {
		first := env.hub.Tunnel(env.hostID)
		if first == nil {
			t.Fatal("parent tunnel peer is missing")
		}
		replacement, err := hostagent.DialTunnelWithOptions(context.Background(), hostagent.TunnelOptions{
			ParentURL: env.server.URL, HostToken: env.hostToken, HTTPClient: env.client,
			DialLocal: func(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
				if hostname != "api.shop.internal" || port != childPort {
					return nil, errTestDial
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, "tcp", childListener.Addr().String())
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = replacement.Close() })
		go func() { _ = replacement.Serve(context.Background()) }()
		waitUntil(t, 2*time.Second, func() bool {
			current := env.hub.Tunnel(env.hostID)
			return current != nil && current != first
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := first.Dial(ctx, "api.shop.internal", childPort); err == nil {
			t.Fatal("replaced tunnel still accepted dials")
		}
		current := env.hub.Tunnel(env.hostID)
		conn, err := current.Dial(ctx, "api.shop.internal", childPort)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got := readEcho(t, conn); got != "child-api" {
			t.Fatalf("reconnected parent→child = %q", got)
		}
		tunnel = replacement
	})

	t.Run("control close tears down tunnel", func(t *testing.T) {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, 2*time.Second, func() bool {
			return !env.hub.Connected(env.hostID) && env.hub.Tunnel(env.hostID) == nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := tunnel.Dial(ctx, "db.shop.internal", parentPort); err == nil {
			t.Fatal("tunnel dial succeeded after control close")
		}
	})
}

func TestHostMeshConnectsOverPrivateHTTP(t *testing.T) {
	env := startHostMeshServer(t, false)
	joinToken, _, err := env.hub.CreateJoinToken(context.Background(), "edge-private", "actor", "admin@example.com", "req-private")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hostagent.Join(context.Background(), hostagent.JoinInput{
		URL: env.server.URL, Token: joinToken, Name: "edge-private", PublicIPv4: "203.0.113.41",
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined.ParentURL != env.server.URL {
		t.Fatalf("parent URL = %q, want %q", joined.ParentURL, env.server.URL)
	}
	connection, _, err := hostagent.DialWithOptions(context.Background(), hostagent.DialOptions{
		ParentURL: joined.ParentURL, HostToken: joined.HostToken, PublicIPv4: "203.0.113.41",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if !env.hub.Connected(joined.HostID) {
		t.Fatal("private HTTP control connection was not attached")
	}
	tunnel, err := hostagent.DialTunnel(context.Background(), joined.ParentURL, joined.HostToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
}

func TestHostMeshTwoChildrenRelay(t *testing.T) {
	env := startHostMesh(t)
	env.dial = func(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
		owned, err := env.store.LookupInternalName(ctx, hostname)
		if err != nil {
			return nil, err
		}
		if !owned.Found || owned.HostID == "" {
			return nil, errTestDial
		}
		peer := env.hub.Tunnel(owned.HostID)
		if peer == nil {
			return nil, ErrHostOffline
		}
		return peer.Dial(ctx, hostname, port)
	}

	first := env.joinChild(t, "edge-1", "203.0.113.40")
	second := env.joinChild(t, "edge-2", "203.0.113.41")
	if _, err := env.store.CreateService(context.Background(), state.CreateService{
		ID: "api", ProjectID: "shop", Name: "api", Enabled: true, HostID: first.hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-api", CreatedAtMillis: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateService(context.Background(), state.CreateService{
		ID: "web", ProjectID: "shop", Name: "web", Enabled: true, HostID: second.hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-web", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-web", CreatedAtMillis: 31,
	}); err != nil {
		t.Fatal(err)
	}

	apiListener := startEcho(t, "child-a-api")
	webListener := startEcho(t, "child-b-web")
	apiPort := uint16(apiListener.Addr().(*net.TCPAddr).Port)
	webPort := uint16(webListener.Addr().(*net.TCPAddr).Port)
	first.dialTunnel(t, env, "api.shop.internal", apiPort, apiListener.Addr().String())
	second.dialTunnel(t, env, "web.shop.internal", webPort, webListener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fromA, err := first.tunnel.Dial(ctx, "web.shop.internal", webPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fromA.Close()
	if got := readEcho(t, fromA); got != "child-b-web" {
		t.Fatalf("child-a→child-b = %q", got)
	}
	fromB, err := second.tunnel.Dial(ctx, "api.shop.internal", apiPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fromB.Close()
	if got := readEcho(t, fromB); got != "child-a-api" {
		t.Fatalf("child-b→child-a = %q", got)
	}
}

type hostMeshEnv struct {
	store     *state.Store
	hub       *Hub
	server    *httptest.Server
	client    *http.Client
	hostname  string
	hostID    string
	hostToken string
	dial      hosttunnel.DialLocal
}

type hostMeshChild struct {
	hostID    string
	hostToken string
	tunnel    *hosttunnel.Peer
}

func startHostMesh(t *testing.T) *hostMeshEnv {
	return startHostMeshServer(t, true)
}

func startHostMeshServer(t *testing.T, secure bool) *hostMeshEnv {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	env := &hostMeshEnv{store: store}

	rawMaster := make([]byte, 32)
	if _, err := rand.Read(rawMaster); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(rawMaster)
	clear(rawMaster)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM, encryptedKey := testOriginCertificate(t, master, "origin-1", []string{"admin.example.com"})
	if err := store.CreateInstallation(context.Background(), state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "origin-1",
		OriginCertificatePEM: certificatePEM, OriginPrivateKey: encryptedKey,
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "shop", Name: "shop", AuditEventID: "audit-project",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateObjectStore(context.Background(), state.CreateObjectStore{
		ID: "db", ProjectID: "shop", Name: "db", BucketName: "shop-db",
		CredentialID: "cred", CredentialName: "root", CredentialPermission: "read_write",
		CredentialSecret: []byte("secret"), CORSOrigins: []string{},
		AuditEventID: "audit-db", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.CreateAnalyticsTracker(context.Background(), state.AnalyticsTracker{
		ID: "tracker", ProjectID: "shop", Name: "shop", RootDomain: "shop.example",
		CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hub, err := New(Config{
		Store: store, Master: master, AdminHostname: listener.Addr().String(),
		DialLocal: func(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
			if env.dial != nil {
				return env.dial(ctx, hostname, port)
			}
			if hostname != "db.shop.internal" {
				return nil, errTestDial
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		},
	})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(hub.Close)

	server := httptest.NewUnstartedServer(hub.Handler())
	_ = server.Listener.Close()
	server.Listener = listener
	server.EnableHTTP2 = false
	server.TLS = &tls.Config{NextProtos: []string{"http/1.1"}}
	if secure {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)

	env.hub = hub
	env.server = server
	env.client = http1Client(server.Client())
	env.hostname = listener.Addr().String()
	return env
}

func (env *hostMeshEnv) joinChild(t *testing.T, name, ipv4 string) *hostMeshChild {
	t.Helper()
	joinToken, _, err := env.hub.CreateJoinToken(context.Background(), name, "actor", "admin@example.com", "req-"+name)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hostagent.Join(context.Background(), hostagent.JoinInput{
		URL: env.server.URL, Token: joinToken, Name: name, PublicIPv4: ipv4, HTTPClient: env.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := hostagent.DialWithOptions(context.Background(), hostagent.DialOptions{
		ParentURL: env.server.URL, HostToken: joined.HostToken, PublicIPv4: ipv4, HTTPClient: env.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() { _ = conn.Serve(context.Background()) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Connected(joined.HostID) })
	return &hostMeshChild{hostID: joined.HostID, hostToken: joined.HostToken}
}

func (child *hostMeshChild) dialTunnel(t *testing.T, env *hostMeshEnv, name string, port uint16, addr string) {
	t.Helper()
	tunnel, err := hostagent.DialTunnelWithOptions(context.Background(), hostagent.TunnelOptions{
		ParentURL: env.server.URL, HostToken: child.hostToken, HTTPClient: env.client,
		DialLocal: func(ctx context.Context, hostname string, got uint16) (net.Conn, error) {
			if hostname != name || got != port {
				return nil, errTestDial
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", addr)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
	go func() { _ = tunnel.Serve(context.Background()) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Tunnel(child.hostID) != nil })
	child.tunnel = tunnel
}

func http1Client(base *http.Client) *http.Client {
	transport, ok := base.Transport.(*http.Transport)
	if !ok {
		return base
	}
	clone := transport.Clone()
	clone.ForceAttemptHTTP2 = false
	clone.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return &http.Client{Transport: clone, Timeout: base.Timeout, CheckRedirect: base.CheckRedirect}
}

func testOriginCertificate(t *testing.T, master cryptobox.MasterKey, id string, names []string) (string, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
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
	encrypted, _, err := origin.EncryptCertificate(master, id, string(certificatePEM), privatePEM, rand.Reader, 1)
	clear(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted.CertificatePEM, encrypted.PrivateKeyEncrypted
}

func startEcho(t *testing.T, body string) net.Listener {
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
				// Keep the backend open until the client closes so the tunnel
				// does not tear the stream down before the response is read.
				_, _ = io.Copy(io.Discard, conn)
			}(conn)
		}
	}()
	return listener
}

func readEcho(t *testing.T, conn net.Conn) string {
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

func waitUntil(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

var errTestDial = ErrHostOffline
