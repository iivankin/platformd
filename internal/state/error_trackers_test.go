package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestCreateErrorTrackerRequiresExistingProject(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()

	_, err := store.CreateErrorTracker(context.Background(), state.CreateErrorTracker{
		ID: "tracker", ProjectID: "missing", Name: "errors", VolumeID: "volume",
		AuditEventID: "audit", ActorKind: "token", ActorID: "token", CreatedAtMillis: 1,
	})
	if !errors.Is(err, state.ErrProjectNotFound) {
		t.Fatalf("create error = %v, want project not found", err)
	}
	var auditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE id = 'audit'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatal("failed error tracker creation wrote an audit event")
	}
}

func TestCreateErrorTrackerPersistsResourceAndAudit(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	tracker, err := store.CreateErrorTracker(ctx, state.CreateErrorTracker{
		ID: "tracker", ProjectID: "project", Name: "errors", VolumeID: "tracker-volume",
		PublicHostname: "Errors.Example.COM", AuditEventID: "tracker-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tracker.ProjectName != "shop" || tracker.PublicHostname != "errors.example.com" || tracker.BackupRetentionCount != state.DefaultBackupRetentionCount {
		t.Fatalf("created tracker = %+v", tracker)
	}
	var action, targetKind string
	if err := store.QueryRowContext(ctx, "SELECT action, target_kind FROM audit_events WHERE id = 'tracker-audit'").Scan(&action, &targetKind); err != nil {
		t.Fatal(err)
	}
	if action != "error_tracker.create" || targetKind != "error_tracker" {
		t.Fatalf("tracker audit = %q/%q", action, targetKind)
	}
}
