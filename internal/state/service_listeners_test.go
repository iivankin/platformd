package state

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestServiceListenersValidateConflictsAndAllowProtocolSpecificPorts(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for index, serviceID := range []string{"api", "worker"} {
		if _, err := store.CreateService(ctx, CreateService{
			ID: serviceID, ProjectID: "project", Name: serviceID, Enabled: true,
			Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
			AuditEventID: "service-audit-" + serviceID, ActorKind: "access",
			ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: int64(index + 2),
		}); err != nil {
			t.Fatal(err)
		}
	}
	attach := func(auditID, serviceID, protocol string, publicPort, targetPort int, timestamp int64) (ServiceListener, error) {
		return store.AttachServiceListener(ctx, AttachServiceListenerInput{
			ProjectID: "project", ServiceID: serviceID, Protocol: protocol,
			PublicPort: publicPort, TargetPort: targetPort, AuditEventID: auditID,
			ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: timestamp,
		})
	}
	tcp, err := attach("tcp-audit", "api", "TCP", 12_345, 8080, 10)
	if err != nil || tcp.Protocol != "tcp" || tcp.TargetPort != 8080 {
		t.Fatalf("TCP listener = %+v, %v", tcp, err)
	}
	udp, err := attach("udp-audit", "api", "udp", 12_345, 5353, 11)
	if err != nil || udp.Protocol != "udp" {
		t.Fatalf("UDP listener on same numeric port = %+v, %v", udp, err)
	}
	updated, err := attach("update-audit", "api", "tcp", 12_345, 9090, 12)
	if err != nil || updated.TargetPort != 9090 || updated.CreatedAt != tcp.CreatedAt {
		t.Fatalf("updated listener = %+v, %v", updated, err)
	}
	_, err = attach("conflict-audit", "worker", "tcp", 12_345, 7000, 13)
	var conflict *ListenerConflict
	if !errors.As(err, &conflict) || conflict.Listener.ServiceID != "api" {
		t.Fatalf("listener conflict = %#v, %v", conflict, err)
	}
	if _, err := attach("reserved-audit", "api", "tcp", 443, 8443, 14); !errors.Is(err, ErrPublicPortReserved) {
		t.Fatalf("reserved port error = %v", err)
	}
	if err := store.DetachServiceListener(ctx, DetachServiceListenerInput{
		ProjectID: "project", ServiceID: "api", Protocol: "tcp", PublicPort: 12_345,
		AuditEventID: "detach-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 15,
	}); err != nil {
		t.Fatal(err)
	}
	listeners, err := store.ServiceListeners(ctx, "project", "api")
	if err != nil || len(listeners) != 1 || listeners[0].Protocol != "udp" {
		t.Fatalf("remaining listeners = %+v, %v", listeners, err)
	}
}

func TestServiceListenersAreUniquePerHostNotGlobally(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	hostID := joinTestHost(t, store, "edge-1", "203.0.113.40", 10)
	if _, err := store.CreateService(ctx, CreateService{
		ID: "api", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, CreateService{
		ID: "worker", ProjectID: "project", Name: "worker", Enabled: true, HostID: hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "audit-worker", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 21,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceListener(ctx, AttachServiceListenerInput{
		ProjectID: "project", ServiceID: "api", Protocol: "tcp", PublicPort: 2222, TargetPort: 22,
		AuditEventID: "audit-primary", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceListener(ctx, AttachServiceListenerInput{
		ProjectID: "project", ServiceID: "worker", Protocol: "tcp", PublicPort: 2222, TargetPort: 22,
		AuditEventID: "audit-child", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 31,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, CreateService{
		ID: "other", ProjectID: "project", Name: "other", Enabled: true, HostID: hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "audit-other", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		CreatedAtMillis: 32,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = store.AttachServiceListener(ctx, AttachServiceListenerInput{
		ProjectID: "project", ServiceID: "other", Protocol: "tcp", PublicPort: 2222, TargetPort: 22,
		AuditEventID: "audit-conflict", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 33,
	})
	var conflict *ListenerConflict
	if !errors.As(err, &conflict) || conflict.Listener.ServiceID != "worker" {
		t.Fatalf("same-host conflict = %#v, %v", conflict, err)
	}

	api, err := store.Service(ctx, "project", "api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateService(ctx, UpdateServiceInput{
		ID: "api", ProjectID: "project", Enabled: true, HostID: hostID, Snapshot: api.Snapshot,
		ExpectedUpdatedMillis: api.UpdatedAtMillis, AuditEventID: "audit-move",
		ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-move", UpdatedAtMillis: 40,
	}); !errors.Is(err, ErrPublicPortUnavailable) {
		t.Fatalf("move onto occupied port = %v", err)
	}
}

func joinTestHost(t *testing.T, store *Store, name, ipv4 string, createdAt int64) string {
	t.Helper()
	tokenID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, secret, err := hosttoken.GenerateJoin(tokenID, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateHostJoinToken(context.Background(), CreateHostJoinTokenInput{
		ID: tokenID, Name: name, TokenHMAC: hosttoken.Digest("join", tokenID, secret),
		ExpiresAtMillis: 100_000, AuditEventID: "audit-token-" + name, ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", RequestCorrelationID: "req-token-" + name,
		CreatedAtMillis: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	hostID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := hosttoken.ParseJoin(plaintext); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeHostJoinToken(context.Background(), ConsumeHostJoinTokenInput{
		TokenID: tokenID, HostID: hostID, AuditEventID: "audit-join-" + name, Name: name,
		PublicIPv4: ipv4, TokenHMAC: hosttoken.Digest("host", hostID, "unused"),
		ConsumedAtMillis: createdAt + 1,
	}); err != nil {
		t.Fatal(err)
	}
	return hostID
}
