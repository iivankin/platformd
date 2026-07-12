package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestCreateAndReadDesiredService(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	port := 8080
	created, err := store.CreateService(context.Background(), CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			ImageReference: "alpine:3.22", Command: []string{"/bin/server"}, Args: []string{"--port", "8080"},
			Environment: map[string]string{"DATABASE_URL": "postgres://db:5432/app"},
			TargetPort:  &port, HealthPath: "/healthz", CPUMillicores: 250, MemoryMaxBytes: 64 << 20,
		},
		AuditEventID: "audit", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "request", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Snapshot.ImageReference != "docker.io/library/alpine:3.22" {
		t.Fatalf("created image = %q", created.Snapshot.ImageReference)
	}
	loaded, err := store.DesiredService(context.Background(), "service")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProjectName != "shop" || loaded.Snapshot.HealthPath != "/healthz" || loaded.Snapshot.CPUMillicores != 250 || loaded.Snapshot.TargetPort == nil || *loaded.Snapshot.TargetPort != 8080 {
		t.Fatalf("loaded service = %+v / %+v", loaded, loaded.Snapshot)
	}
	var auditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE action = 'service.create' AND target_id = 'service'").Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit count = %d, %v", auditCount, err)
	}
}

func TestCreateServiceEnforcesProjectResourceNamespace(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO object_stores(id, project_id, name, bucket_name, created_at, updated_at)
VALUES ('store', 'project', 'assets', 'assets', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateService(context.Background(), CreateService{
		ID: "service", ProjectID: "project", Name: "assets", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{ImageReference: "alpine"},
		AuditEventID: "audit", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if !errors.Is(err, ErrResourceNameConflict) {
		t.Fatalf("error = %v, want ErrResourceNameConflict", err)
	}
}
