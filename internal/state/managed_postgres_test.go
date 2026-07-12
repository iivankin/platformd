package state_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

const managedPostgresTestDigest = "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4"

func TestCreateManagedPostgresPersistsProfileAndAudit(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManagedPostgres(context.Background(), state.CreateManagedPostgres{
		ID: "postgres", ProjectID: "project", Name: "primary", ImageTag: "18.3",
		ImageDigest: managedPostgresTestDigest, VolumeID: "volume", DatabaseName: "app_postgres",
		OwnerUsername: "owner_postgres", OwnerPasswordEncrypted: []byte("owner-secret"),
		BootstrapPasswordEncrypted: []byte("bootstrap-secret"), CPUMillicores: 500,
		MemoryMaxBytes: 1 << 30, AuditEventID: "audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "admin@example.com", RequestCorrelationID: "request",
		CreatedAtMillis: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ProjectName != "shop" || created.DatabaseName != "app_postgres" || created.OwnerUsername != "owner_postgres" || created.CPUMillicores != 500 || created.MemoryMaxBytes != 1<<30 {
		t.Fatalf("created managed PostgreSQL = %+v", created)
	}
	resources, err := store.ManagedPostgresByProject(context.Background(), "project")
	if err != nil || len(resources) != 1 || resources[0].ID != created.ID {
		t.Fatalf("managed PostgreSQL list = %+v, %v", resources, err)
	}
	var action, targetKind, targetID string
	if err := store.QueryRowContext(context.Background(), `
SELECT action, target_kind, target_id FROM audit_events WHERE id = 'audit'`).Scan(&action, &targetKind, &targetID); err != nil {
		t.Fatal(err)
	}
	if action != "postgres.create" || targetKind != "postgres" || targetID != created.ID {
		t.Fatalf("managed PostgreSQL audit = %q/%q/%q", action, targetKind, targetID)
	}
}
