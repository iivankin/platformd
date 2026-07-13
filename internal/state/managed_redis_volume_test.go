package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestSwitchManagedRedisVolumeIsAtomicAndOptimistic(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "old-volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "create-audit",
		ActorKind: "token", ActorID: "token", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: "redis", ExpectedVolumeID: "old-volume", VolumeID: "new-volume",
		Action: "redis.restore", AuditEventID: "restore-audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "user@example.com", RequestCorrelationID: "request",
		UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	resource, err := store.ManagedRedis(ctx, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if resource.VolumeID != "new-volume" || resource.UpdatedAtMillis != 3 {
		t.Fatalf("switched managed Redis = %+v", resource)
	}
	var action, requestID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT action, request_correlation_id, metadata_json
FROM audit_events WHERE id = 'restore-audit'`).Scan(&action, &requestID, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "redis.restore" || requestID != "request" ||
		metadata != `{"actorEmail":"user@example.com","previousVolumeId":"old-volume","volumeId":"new-volume"}` {
		t.Fatalf("restore audit = %q %q %s", action, requestID, metadata)
	}
	if err := store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: "redis", ExpectedVolumeID: "new-volume", VolumeID: "recovery-volume",
		Action: "redis.restore", AuditEventID: "recovery-audit", ActorKind: "system",
		ActorID: "disaster_restore", UpdatedAtMillis: 4,
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
	if err := store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: "redis", ExpectedVolumeID: "recovery-volume", VolumeID: "forbidden-volume",
		Action: "redis.version_change", AuditEventID: "forbidden-audit", ActorKind: "system",
		ActorID: "disaster_restore", UpdatedAtMillis: 5,
	}); err == nil {
		t.Fatal("system actor was allowed to perform a version change")
	}

	err = store.SwitchManagedRedisVolume(ctx, state.SwitchManagedRedisVolume{
		ResourceID: "redis", ExpectedVolumeID: "old-volume", VolumeID: "other-volume",
		Action: "redis.restore", AuditEventID: "stale-audit", ActorKind: "token",
		ActorID: "token", UpdatedAtMillis: 6,
	})
	if err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("stale switch error = %v", err)
	}
	var staleAuditCount int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE id = 'stale-audit'").Scan(&staleAuditCount); err != nil {
		t.Fatal(err)
	}
	if staleAuditCount != 0 {
		t.Fatal("failed volume switch committed an audit event")
	}
}
