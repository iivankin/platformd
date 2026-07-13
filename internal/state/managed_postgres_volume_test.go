package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestSwitchManagedPostgresVolumeIsAtomicAndOptimistic(t *testing.T) {
	t.Parallel()
	const initialDigest = "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4"
	const targetDigest = "sha256:4b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a5"
	const rejectedDigest = "sha256:5b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a6"
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
		ImageDigest: initialDigest,
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
	if err := store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "new-volume", VolumeID: "version-volume",
		ExpectedImageTag: "17", ExpectedImageDigest: initialDigest,
		ImageTag: "18", ImageDigest: targetDigest,
		Action: "postgres.version_change", AuditEventID: "version-audit", ActorKind: "token",
		ActorID: "token", RequestCorrelationID: "version-request", UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	resource, err = store.ManagedPostgres(ctx, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if resource.VolumeID != "version-volume" || resource.ImageTag != "18" || resource.ImageDigest != targetDigest || resource.UpdatedAtMillis != 4 {
		t.Fatalf("version-switched managed PostgreSQL = %+v", resource)
	}
	if err := store.QueryRowContext(ctx, `SELECT metadata_json FROM audit_events WHERE id = 'version-audit'`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, `"previousImageTag":"17"`) || !strings.Contains(metadata, `"imageTag":"18"`) ||
		!strings.Contains(metadata, targetDigest) {
		t.Fatalf("version audit metadata = %s", metadata)
	}
	if err := store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "version-volume", VolumeID: "recovery-volume",
		Action: "postgres.restore", AuditEventID: "recovery-audit", ActorKind: "system",
		ActorID: "disaster_restore", UpdatedAtMillis: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var actorKind, actorID string
	if err := store.QueryRowContext(ctx, `
SELECT actor_kind, actor_id FROM audit_events WHERE id = 'recovery-audit'`).Scan(&actorKind, &actorID); err != nil {
		t.Fatal(err)
	}
	if actorKind != "system" || actorID != "disaster_restore" {
		t.Fatalf("recovery actor = %q/%q", actorKind, actorID)
	}
	if err := store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "recovery-volume", VolumeID: "forbidden-volume",
		ExpectedImageTag: "18", ExpectedImageDigest: targetDigest,
		ImageTag: "19", ImageDigest: rejectedDigest,
		Action: "postgres.version_change", AuditEventID: "forbidden-audit", ActorKind: "system",
		ActorID: "disaster_restore", UpdatedAtMillis: 6,
	}); err == nil {
		t.Fatal("system actor was allowed to perform a version change")
	}
	err = store.SwitchManagedPostgresVolume(ctx, state.SwitchManagedPostgresVolume{
		ResourceID: "postgres", ExpectedVolumeID: "old-volume", VolumeID: "other-volume",
		Action: "postgres.restore", AuditEventID: "stale-audit", ActorKind: "token",
		ActorID: "token", UpdatedAtMillis: 7,
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
