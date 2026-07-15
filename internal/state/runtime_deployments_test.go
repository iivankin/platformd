package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeDeploymentLifecyclePreservesStoppedCurrentAndDeletesOldHistory(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	first := RuntimeDeployment{
		ID: "first", ResourceKind: "postgres", ResourceID: "database",
		ImageTag: "16", ImageDigest: "sha256:first", CreatedAtMillis: 1,
	}
	if err := store.BeginRuntimeDeployment(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateRuntimeDeployment(ctx, "postgres", "database", first.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.StopRuntimeDeployment(ctx, "postgres", "database", first.ID, 3); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.ActiveRuntimeDeployment(ctx, "postgres", "database")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "removed" || !stopped.Active {
		t.Fatalf("stopped deployment = %+v", stopped)
	}
	if err := store.RestartRuntimeDeployment(ctx, "postgres", "database", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateRuntimeDeployment(ctx, "postgres", "database", first.ID, 4); err != nil {
		t.Fatal(err)
	}

	second := RuntimeDeployment{
		ID: "second", ResourceKind: "postgres", ResourceID: "database",
		ImageTag: "17", ImageDigest: "sha256:second", CreatedAtMillis: 5,
	}
	if err := store.BeginRuntimeDeployment(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateRuntimeDeployment(ctx, "postgres", "database", second.ID, 6); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRuntimeDeployment(ctx, "postgres", "database", second.ID); !errors.Is(err, ErrRuntimeDeploymentNotFound) {
		t.Fatalf("delete current deployment error = %v", err)
	}
	if err := store.DeleteRuntimeDeployment(ctx, "postgres", "database", first.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.RuntimeDeployments(ctx, "postgres", "database", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Deployments) != 1 || page.Deployments[0].ID != second.ID || !page.Deployments[0].Active {
		t.Fatalf("deployment history = %+v", page.Deployments)
	}
}

func TestFailedRuntimeDeploymentStaysInHistoryWithoutReplacingCurrent(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	for _, deployment := range []RuntimeDeployment{
		{ID: "current", ResourceKind: "redis", ResourceID: "cache", ImageTag: "8", ImageDigest: "sha256:current", CreatedAtMillis: 1},
		{ID: "candidate", ResourceKind: "redis", ResourceID: "cache", ImageTag: "9", ImageDigest: "sha256:candidate", CreatedAtMillis: 3},
	} {
		if err := store.BeginRuntimeDeployment(ctx, deployment); err != nil {
			t.Fatal(err)
		}
		if deployment.ID == "current" {
			if err := store.ActivateRuntimeDeployment(ctx, "redis", "cache", deployment.ID, 2); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.FailRuntimeDeployment(ctx, "candidate", "readiness_failed", "timeout", 4); err != nil {
		t.Fatal(err)
	}
	active, err := store.ActiveRuntimeDeployment(ctx, "redis", "cache")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "current" {
		t.Fatalf("active deployment = %+v", active)
	}
	failed, err := store.RuntimeDeployment(ctx, "redis", "cache", "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.Active || failed.ErrorCode != "readiness_failed" {
		t.Fatalf("failed deployment = %+v", failed)
	}
}
