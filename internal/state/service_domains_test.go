package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestServiceDomainAttachRequiresExplicitMoveAndTargetPort(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, project := range []CreateProject{
		{ID: "project-a", Name: "alpha", AuditEventID: "project-audit-a", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1},
		{ID: "project-b", Name: "beta", AuditEventID: "project-audit-b", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2},
	} {
		if _, err := store.CreateProject(context.Background(), project); err != nil {
			t.Fatal(err)
		}
	}
	port := 8080
	for _, service := range []CreateService{
		{ID: "service-a", ProjectID: "project-a", Name: "api", Enabled: true, Snapshot: serviceconfig.Snapshot{ImageReference: "alpine", TargetPort: &port}, AuditEventID: "service-audit-a", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 3},
		{ID: "service-b", ProjectID: "project-b", Name: "web", Enabled: true, Snapshot: serviceconfig.Snapshot{ImageReference: "alpine", TargetPort: &port}, AuditEventID: "service-audit-b", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 4},
		{ID: "service-no-port", ProjectID: "project-a", Name: "worker", Enabled: true, Snapshot: serviceconfig.Snapshot{ImageReference: "alpine"}, AuditEventID: "service-audit-worker", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 5},
	} {
		if _, err := store.CreateService(context.Background(), service); err != nil {
			t.Fatal(err)
		}
	}
	attached, err := store.AttachServiceDomain(context.Background(), AttachServiceDomainInput{
		ProjectID: "project-a", ServiceID: "service-a", Hostname: "App.Example.com",
		AuditEventID: "attach-audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 6,
	})
	if err != nil || attached.Hostname != "app.example.com" || attached.ServiceID != "service-a" {
		t.Fatalf("attached domain = %+v, %v", attached, err)
	}
	_, err = store.AttachServiceDomain(context.Background(), AttachServiceDomainInput{
		ProjectID: "project-b", ServiceID: "service-b", Hostname: "app.example.com",
		AuditEventID: "conflict-audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 7,
	})
	var conflict *DomainConflict
	if !errors.As(err, &conflict) || conflict.Domain.ProjectID != "project-a" {
		t.Fatalf("domain conflict = %#v, %v", conflict, err)
	}
	moved, err := store.AttachServiceDomain(context.Background(), AttachServiceDomainInput{
		ProjectID: "project-b", ServiceID: "service-b", Hostname: "app.example.com", Move: true,
		AuditEventID: "move-audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 8,
	})
	if err != nil || moved.ServiceID != "service-b" {
		t.Fatalf("moved domain = %+v, %v", moved, err)
	}
	if err := store.DetachServiceDomain(context.Background(), DetachServiceDomainInput{
		ProjectID: "project-b", ServiceID: "service-b", Hostname: "app.example.com",
		AuditEventID: "detach-audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceDomain(context.Background(), AttachServiceDomainInput{
		ProjectID: "project-a", ServiceID: "service-no-port", Hostname: "worker.example.com",
		AuditEventID: "port-audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 10,
	}); !errors.Is(err, ErrServiceTargetPortNeeded) {
		t.Fatalf("missing target port error = %v", err)
	}
}
