package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
			Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine"),},
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
