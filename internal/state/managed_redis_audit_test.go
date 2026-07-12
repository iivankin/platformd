package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestManagedRedisDataAuditOmitsKeysAndValues(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "create-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordManagedRedisDataMutation(ctx, state.RecordManagedRedisDataMutation{
		ResourceID: "redis", ProjectID: "project", Operation: "hash_set", Result: "succeeded",
		AuditEventID: "data-audit", ActorID: "user", ActorEmail: "user@example.com",
		RequestCorrelationID: "request", CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	var action, metadata string
	if err := store.QueryRowContext(ctx, "SELECT action, metadata_json FROM audit_events WHERE id = 'data-audit'").Scan(&action, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "redis.data.mutate" || !strings.Contains(metadata, `"operation":"hash_set"`) || strings.Contains(metadata, "key") || strings.Contains(metadata, "value") {
		t.Fatalf("unexpected data audit: %s %s", action, metadata)
	}
}
