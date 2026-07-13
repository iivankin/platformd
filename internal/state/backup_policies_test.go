package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestBackupPolicyUsesResourceRowAndCanonicalCron(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "create-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := store.SetBackupPolicy(ctx, state.SetBackupPolicy{
		ResourceKind: "redis", ResourceID: "redis", Enabled: true,
		Cron: "  5   */2 * * 1-5 ", RetentionCount: 12, AuditEventID: "policy-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com",
		RequestCorrelationID: "request", UpdatedAtMillis: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Enabled || policy.Cron != "5 */2 * * 1-5" || policy.RetentionCount != 12 {
		t.Fatalf("policy = %+v", policy)
	}
	resource, err := store.ManagedRedis(ctx, "redis")
	if err != nil || !resource.BackupEnabled || resource.BackupCron != policy.Cron || resource.BackupRetentionCount != 12 {
		t.Fatalf("resource policy = %+v, %v", resource, err)
	}
	policies, err := store.BackupPolicies(ctx)
	if err != nil || len(policies) != 1 || policies[0] != policy {
		t.Fatalf("policies = %+v, %v", policies, err)
	}
	var action, targetKind string
	if err := store.QueryRowContext(ctx, "SELECT action, target_kind FROM audit_events WHERE id = 'policy-audit'").Scan(&action, &targetKind); err != nil {
		t.Fatal(err)
	}
	if action != "backup.policy.set" || targetKind != "redis" {
		t.Fatalf("policy audit = %q/%q", action, targetKind)
	}
}

func TestBackupPolicyValidationAndScheduledOccurrenceLookup(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	createManagedRedisTestProject(t, store)
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "create-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := store.SetBackupPolicy(ctx, state.SetBackupPolicy{
		ResourceKind: "redis", ResourceID: "redis", Enabled: true, RetentionCount: 7,
		AuditEventID: "policy-audit", ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com",
		UpdatedAtMillis: 20,
	})
	if err == nil {
		t.Fatal("enabled policy without cron was accepted")
	}
	occurrence := int64(1000)
	if err := store.BeginBackup(ctx, state.BeginBackup{
		ID: "backup", ResourceKind: "redis", ResourceID: "redis", GenerationID: "generation",
		ScheduledOccurrenceMillis: &occurrence, StartedAtMillis: 1001,
	}); err != nil {
		t.Fatal(err)
	}
	exists, err := store.ScheduledBackupExists(ctx, "redis", "redis", occurrence)
	if err != nil || !exists {
		t.Fatalf("scheduled occurrence exists = %v, %v", exists, err)
	}
	exists, err = store.ScheduledBackupExists(ctx, "redis", "redis", occurrence+1)
	if err != nil || exists {
		t.Fatalf("different occurrence exists = %v, %v", exists, err)
	}
	if _, err := store.BackupPolicy(ctx, "redis", "missing"); !errors.Is(err, state.ErrBackupResourceNotFound) {
		t.Fatalf("missing policy error = %v", err)
	}
}
