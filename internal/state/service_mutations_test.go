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

func TestDeployVersionCopiesSnapshotAndPinsDigest(t *testing.T) {
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
	deployedVersion, err := store.DeployServiceVersion(context.Background(), DeployServiceVersionInput{
		ID: service.ID, ProjectID: service.ProjectID, DeploymentID: "deployment",
		ExpectedUpdatedMillis: changed.UpdatedAtMillis,
		AuditEventID:          "deploy-version-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(deployedVersion.Snapshot.ImageReference, "@"+testImageDigest) {
		t.Fatalf("deployed version image reference = %q", deployedVersion.Snapshot.ImageReference)
	}
	if deployedVersion.ActiveDeploymentID != "deployment" || !deployedVersion.Enabled {
		t.Fatalf("deploying a version changed service-level state: %+v", deployedVersion)
	}
	var deployVersionAuditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE id = 'deploy-version-audit' AND action = 'service.deploy_version'").Scan(&deployVersionAuditCount); err != nil || deployVersionAuditCount != 1 {
		t.Fatalf("deploy version audit count = %d, %v", deployVersionAuditCount, err)
	}
}

func TestDeployVersionAcceptsFailedDeploymentForRetry(t *testing.T) {
	store := serviceMutationStore(t)
	defer store.Close()
	service := createMutationService(t, store, "alpine:3.22")
	_, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "failed-deployment", ServiceID: service.ID, ImageDigest: testImageDigest,
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailDeployment(context.Background(), "failed-deployment", "container_exit", "exit 1", 4); err != nil {
		t.Fatal(err)
	}
	current, err := store.DesiredService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := store.DeployServiceVersion(context.Background(), DeployServiceVersionInput{
		ID: service.ID, ProjectID: service.ProjectID, DeploymentID: "failed-deployment",
		ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID:          "retry-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(retried.Snapshot.ImageReference, "@"+testImageDigest) || !retried.Enabled {
		t.Fatalf("retried service = %+v", retried)
	}
}

func TestDeleteServiceDeploymentRemovesOnlyInactiveHistory(t *testing.T) {
	store := serviceMutationStore(t)
	defer store.Close()
	service := createMutationService(t, store, "alpine:3.22")
	_, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for index, deploymentID := range []string{"old-deployment", "active-deployment"} {
		if err := store.BeginDeployment(context.Background(), BeginDeployment{
			ID: deploymentID, ServiceID: service.ID, ImageDigest: testImageDigest,
			ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: int64(3 + index*2),
		}); err != nil {
			t.Fatal(err)
		}
		expectedActive := ""
		if index > 0 {
			expectedActive = "old-deployment"
		}
		if err := store.ActivateDeployment(context.Background(), service.ID, deploymentID, expectedActive, int64(4+index*2)); err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.DesiredService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := DeleteServiceDeploymentInput{
		ID: service.ID, ProjectID: service.ProjectID, ExpectedUpdatedMillis: current.UpdatedAtMillis,
		AuditEventID: "remove-active-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 7,
		DeploymentID: "active-deployment",
	}
	if err := store.DeleteServiceDeployment(context.Background(), input); !errors.Is(err, ErrDeploymentIsActive) {
		t.Fatalf("delete active deployment error = %v", err)
	}
	input.DeploymentID = "old-deployment"
	input.AuditEventID = "remove-old-audit"
	if err := store.DeleteServiceDeployment(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ServiceDeployment(context.Background(), service.ProjectID, service.ID, "old-deployment"); !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("load removed deployment error = %v", err)
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
