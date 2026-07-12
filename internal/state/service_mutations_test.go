package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

const testImageDigest = "sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"

func TestUpdateServiceUsesOptimisticVersionAndClearsActivePointerWhenDisabled(t *testing.T) {
	store := serviceMutationStore(t)
	defer store.Close()
	service := createMutationService(t, store, "alpine:3.22")
	normalized, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "deployment", ServiceID: service.ID, ImageDigest: testImageDigest,
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(context.Background(), service.ID, "deployment", "", 4); err != nil {
		t.Fatal(err)
	}
	active, err := store.DesiredService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateService(context.Background(), UpdateServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, Enabled: false,
		Snapshot: normalized, ExpectedUpdatedMillis: active.UpdatedAtMillis,
		AuditEventID: "update-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		UpdatedAtMillis: active.UpdatedAtMillis,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.ActiveDeploymentID != "" || updated.UpdatedAtMillis <= active.UpdatedAtMillis {
		t.Fatalf("updated service = %+v", updated)
	}
	_, err = store.UpdateService(context.Background(), UpdateServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, Enabled: true,
		Snapshot: normalized, ExpectedUpdatedMillis: active.UpdatedAtMillis,
		AuditEventID: "stale-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		UpdatedAtMillis: 10,
	})
	if !errors.Is(err, ErrServiceChanged) {
		t.Fatalf("stale update error = %v", err)
	}
	var staleAuditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE id = 'stale-audit'").Scan(&staleAuditCount); err != nil || staleAuditCount != 0 {
		t.Fatalf("stale audit count = %d, %v", staleAuditCount, err)
	}
}

func TestRollbackCopiesSuccessfulSnapshotAndPinsDigest(t *testing.T) {
	store := serviceMutationStore(t)
	defer store.Close()
	service := createMutationService(t, store, "alpine:3.22")
	_, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "deployment", ServiceID: service.ID, ImageDigest: testImageDigest,
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(context.Background(), service.ID, "deployment", "", 4); err != nil {
		t.Fatal(err)
	}
	active, err := store.DesiredService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.UpdateService(context.Background(), UpdateServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, Enabled: true,
		Snapshot:              serviceconfig.Snapshot{ImageReference: "alpine:3.23"},
		ExpectedUpdatedMillis: active.UpdatedAtMillis,
		AuditEventID:          "update-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := store.RollbackService(context.Background(), RollbackServiceInput{
		ID: service.ID, ProjectID: service.ProjectID, DeploymentID: "deployment",
		ExpectedUpdatedMillis: changed.UpdatedAtMillis,
		AuditEventID:          "rollback-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rolledBack.Snapshot.ImageReference, "@"+testImageDigest) {
		t.Fatalf("rollback image reference = %q", rolledBack.Snapshot.ImageReference)
	}
	if rolledBack.ActiveDeploymentID != "deployment" || !rolledBack.Enabled {
		t.Fatalf("rollback changed service-level state: %+v", rolledBack)
	}
	var rollbackAuditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE id = 'rollback-audit' AND action = 'service.rollback'").Scan(&rollbackAuditCount); err != nil || rollbackAuditCount != 1 {
		t.Fatalf("rollback audit count = %d, %v", rollbackAuditCount, err)
	}
}

func serviceMutationStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(context.Background(), CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func createMutationService(t *testing.T, store *Store, imageReference string) ServiceDesired {
	t.Helper()
	service, err := store.CreateService(context.Background(), CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{ImageReference: imageReference},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
