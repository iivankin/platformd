package hosthub

import (
	"context"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/hostagent"
	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/state"
)

func TestHostServiceStatusReportsAndDisconnect(t *testing.T) {
	env := startHostMesh(t)
	joinToken, _, err := env.hub.CreateJoinToken(context.Background(), "edge-status", "actor", "admin@example.com", "req-status")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hostagent.Join(context.Background(), hostagent.JoinInput{
		URL: env.server.URL, Token: joinToken, Name: "edge-status",
		PublicIPv4: "203.0.113.50", HTTPClient: env.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := hostagent.DialWithOptions(context.Background(), hostagent.DialOptions{
		ParentURL: env.server.URL, HostToken: joined.HostToken, PublicIPv4: "203.0.113.50",
		HTTPClient: env.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	t.Cleanup(func() {
		stopServe()
		_ = conn.Close()
	})
	go func() { _ = conn.Serve(serveCtx) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Connected(joined.HostID) })

	status, message := env.hub.ServiceStatus(joined.HostID, "api", false)
	if status != "disabled" || message != "" {
		t.Fatalf("disabled = %q/%q", status, message)
	}
	status, message = env.hub.ServiceStatus(joined.HostID, "api", true)
	if status != "pending" || message != childAwaitingStatusMessage {
		t.Fatalf("awaiting = %q/%q", status, message)
	}
	status, message = env.hub.ServiceStatus("missing-host", "api", true)
	if status != "pending" || message != childOfflineMessage {
		t.Fatalf("missing host = %q/%q", status, message)
	}

	if err := conn.Write(context.Background(), hostconn.KindStatus, hostconn.Status{
		Services: []hostconn.ServiceRuntime{
			{ServiceID: "api", Status: "running"},
			{ServiceID: "web", Status: "failed", Message: "crash"},
			{ServiceID: "bad", Status: "nope"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool {
		status, _ := env.hub.ServiceStatus(joined.HostID, "api", true)
		return status == "running"
	})
	status, message = env.hub.ServiceStatus(joined.HostID, "web", true)
	if status != "failed" || message != "crash" {
		t.Fatalf("web = %q/%q", status, message)
	}
	status, message = env.hub.ServiceStatus(joined.HostID, "bad", true)
	if status != "pending" || message != childAwaitingStatusMessage {
		t.Fatalf("invalid status = %q/%q", status, message)
	}

	if err := conn.Write(context.Background(), hostconn.KindStatus, hostconn.Status{
		Services: []hostconn.ServiceRuntime{{ServiceID: "web", Status: "pending", Message: "restarting"}},
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, time.Second, func() bool {
		status, _ := env.hub.ServiceStatus(joined.HostID, "api", true)
		web, _ := env.hub.ServiceStatus(joined.HostID, "web", true)
		return status == "pending" && web == "pending"
	})
	status, message = env.hub.ServiceStatus(joined.HostID, "api", true)
	if status != "pending" || message != childAwaitingStatusMessage {
		t.Fatalf("removed service = %q/%q", status, message)
	}
	status, message = env.hub.ServiceStatus(joined.HostID, "web", true)
	if status != "pending" || message != "restarting" {
		t.Fatalf("replaced web = %q/%q", status, message)
	}
	if err := conn.Write(context.Background(), hostconn.KindHeartbeat, struct{}{}); err != nil {
		t.Fatal(err)
	}
	// This RPC is ordered after the heartbeat on the same connection, so its
	// reply proves that the parent processed the heartbeat first.
	if _, err := hostagent.LookupInternal(context.Background(), conn, "missing.shop.internal"); err != nil {
		t.Fatal(err)
	}
	status, message = env.hub.ServiceStatus(joined.HostID, "web", true)
	if status != "pending" || message != "restarting" {
		t.Fatalf("heartbeat changed web = %q/%q", status, message)
	}

	stopServe()
	_ = conn.Close()
	waitUntil(t, time.Second, func() bool { return !env.hub.Connected(joined.HostID) })
	status, message = env.hub.ServiceStatus(joined.HostID, "web", true)
	if status != "pending" || message != childOfflineMessage {
		t.Fatalf("disconnected = %q/%q", status, message)
	}
}

func TestHostMeshPushesCatalogAndDisconnectsDeletedHost(t *testing.T) {
	env := startHostMesh(t)
	joinToken, _, err := env.hub.CreateJoinToken(context.Background(), "edge-catalog", "actor", "admin@example.com", "req-catalog")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := hostagent.Join(context.Background(), hostagent.JoinInput{
		URL: env.server.URL, Token: joinToken, Name: "edge-catalog",
		PublicIPv4: "203.0.113.51", HTTPClient: env.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	projects := make(chan []state.RuntimeProject, 1)
	certificates := make(chan []hostconn.CertificatePEM, 1)
	conn, welcome, err := hostagent.DialWithOptions(context.Background(), hostagent.DialOptions{
		ParentURL: env.server.URL, HostToken: joined.HostToken, PublicIPv4: "203.0.113.51",
		HTTPClient: env.client,
		Handlers: hostagent.Handlers{
			Projects: func(received []state.RuntimeProject) error {
				projects <- received
				return nil
			},
			Certificates: func(received []hostconn.CertificatePEM) error {
				certificates <- received
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if len(welcome.Certificates) != 1 {
		t.Fatalf("welcome certificates = %d", len(welcome.Certificates))
	}
	go func() { _ = conn.Serve(context.Background()) }()
	waitUntil(t, time.Second, func() bool { return env.hub.Connected(joined.HostID) })

	env.hub.PushProjects([]state.RuntimeProject{{ID: "blog", Name: "blog", ObjectStoreEnabled: true}})
	select {
	case received := <-projects:
		if len(received) != 1 || received[0].ID != "blog" || received[0].Name != "blog" || received[0].ObjectStoreEnabled {
			t.Fatalf("projects = %+v", received)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("projects were not delivered")
	}

	if err := env.hub.PushCertificates(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-certificates:
		if len(received) != 1 || received[0].ID != welcome.Certificates[0].ID || received[0].PrivateKeyPEM == "" {
			t.Fatalf("certificates = %+v", received)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("certificates were not delivered")
	}

	if err := env.hub.DeleteHost(context.Background(), state.DeleteHostInput{
		ID: joined.HostID, AuditEventID: "audit-delete-host", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", RequestCorrelationID: "req-delete-host",
		DeletedAtMillis: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool { return !env.hub.Connected(joined.HostID) })
}
