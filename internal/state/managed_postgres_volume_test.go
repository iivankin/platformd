package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestSwitchManagedPostgresVolumeIsAtomicAndOptimistic(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "demo", AuditEventID: "project-audit",
		ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedPostgres(ctx, state.CreateManagedPostgres{
		ID: "postgres", ProjectID: "project", Name: "database", ImageTag: "17",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "old-volume", DatabaseName: "app", OwnerUsername: "owner",
		OwnerPasswordEncrypted: []byte("owner"), BootstrapPasswordEncrypted: []byte("bootstrap"),
		AuditEventID: "create-audit", ActorKind: "token", ActorID: "token", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "old-volume", VolumeID: "new-volume",
		Action: "postgres.restore", AuditEventID: "restore-audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "user@example.com", RequestCorrelationID: "request",
		UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	resource, err := store.ManagedPostgres(ctx, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if resource.VolumeID != "new-volume" || resource.UpdatedAtMillis != 3 {
		t.Fatalf("switched managed PostgreSQL = %+v", resource)
	}
	var action, requestID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT action, request_correlation_id, metadata_json
FROM audit_events WHERE id = 'restore-audit'`).Scan(&action, &requestID, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "postgres.restore" || requestID != "request" ||
		metadata != `{"actorEmail":"user@example.com","previousVolumeId":"old-volume","volumeId":"new-volume"}` {
		t.Fatalf("restore audit = %q %q %s", action, requestID, metadata)
	}
	err = store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "old-volume", VolumeID: "other-volume",
		Action: "postgres.restore", AuditEventID: "stale-audit", ActorKind: "token",
		ActorID: "token", UpdatedAtMillis: 4,
	})
	if err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("stale switch error = %v", err)
	}
	var count int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE id = 'stale-audit'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed managed PostgreSQL switch committed an audit event")
	}
}
