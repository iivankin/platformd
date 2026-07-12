package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestCreateManagedRedisIsProjectScopedAndAudited(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)

	resource, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), CPUMillicores: 500,
		MemoryMaxBytes: 128 << 20, AuditEventID: "audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.ProjectName != "demo" || resource.ImageTag != "7.4" || resource.CPUMillicores != 500 || resource.BackupRetentionCount != 7 {
		t.Fatalf("unexpected managed Redis resource: %+v", resource)
	}
	listed, err := store.ManagedRedisByProject(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != resource.ID {
		t.Fatalf("unexpected managed Redis list: %+v", listed)
	}
	var action, targetKind, targetID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT action, target_kind, target_id, metadata_json FROM audit_events WHERE id = 'audit'`).Scan(&action, &targetKind, &targetID, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "redis.create" || targetKind != "redis" || targetID != "redis" || metadata != `{"actorEmail":"user@example.com"}` {
		t.Fatalf("unexpected audit: %q %q %q %s", action, targetKind, targetID, metadata)
	}
}

func TestCreateManagedRedisRejectsSharedNamespaceAndRollsBackAudit(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)
	if _, err := store.CreateService(ctx, state.CreateService{
		ID: "service", ProjectID: "project", Name: "cache", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{ImageReference: "redis:latest"},
		AuditEventID: "service-audit", ActorKind: "token", ActorID: "token",
		CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "latest",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "audit",
		ActorKind: "token", ActorID: "token", CreatedAtMillis: 10,
	})
	if !errors.Is(err, state.ErrResourceNameConflict) {
		t.Fatalf("create error = %v, want resource name conflict", err)
	}
	var count int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE id = 'audit'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed managed Redis creation wrote an audit event")
	}
}

func createManagedRedisTestProject(t *testing.T, store *state.Store) {
	t.Helper()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "demo", AuditEventID: "project-audit",
		ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
}
