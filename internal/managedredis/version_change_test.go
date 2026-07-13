package managedredis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const versionChangeDigest = "sha256:8f44c86427f46662be02d9f86a87dbd45c0caa796fa8fb4f33a2f0704e9e97f1"

func TestChangeVersionCopiesRDBAndSwitchesImageVolumePointer(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t, nil)
	fixture.engine.image.ID = "target-image-id"
	fixture.engine.image.Digest = versionChangeDigest
	progress := make([]string, 0)
	err := fixture.controller.ChangeVersion(context.Background(), VersionChangeInput{
		ResourceID: "redis-id", ImageTag: "8.0", ImageDigest: versionChangeDigest,
		Actor:    Actor{Kind: "token", ID: "admin-token"},
		Progress: func(value string) { progress = append(progress, value) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.store.resource.ImageTag != "8.0" || fixture.store.resource.ImageDigest != versionChangeDigest ||
		fixture.store.resource.VolumeID != "new-volume" {
		t.Fatalf("version-changed resource = %+v", fixture.store.resource)
	}
	switchInput := fixture.store.switchInput
	if switchInput.Action != "redis.version_change" || switchInput.ExpectedImageTag != "7.4" ||
		switchInput.ExpectedImageDigest != testImageDigest || switchInput.ImageTag != "8.0" ||
		switchInput.ImageDigest != versionChangeDigest || switchInput.ActorKind != "token" {
		t.Fatalf("version switch = %+v", switchInput)
	}
	value, err := os.ReadFile(filepath.Join(fixture.volumeRoot, "project-id", "new-volume", "dump.rdb"))
	if err != nil || string(value) != "old-rdb" {
		t.Fatalf("copied RDB = %q, %v", value, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old volume survived: %v", err)
	}
	if !reflect.DeepEqual(progress, []string{
		"resolving_target_image", "withdrawing_endpoint", "draining_clients", "saving_source", "copying_rdb",
		"validating_target", "switching_active_pointer", "complete",
	}) {
		t.Fatalf("progress = %v", progress)
	}
	if len(fixture.engine.created) != 1 || fixture.engine.created[0].ImageID != "target-image-id" {
		t.Fatalf("candidate specs = %+v", fixture.engine.created)
	}
}

func TestChangeVersionRestoresOldRuntimeWhenTargetFails(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t, errors.New("forced switch failure"))
	fixture.engine.image.ID = "target-image-id"
	fixture.engine.image.Digest = versionChangeDigest
	err := fixture.controller.ChangeVersion(context.Background(), VersionChangeInput{
		ResourceID: "redis-id", ImageTag: "8.0", ImageDigest: versionChangeDigest,
		Actor: Actor{Kind: "access", ID: "user", Email: "user@example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "forced switch failure") {
		t.Fatalf("version change error = %v", err)
	}
	if fixture.store.resource.ImageTag != "7.4" || fixture.store.resource.VolumeID != "old-volume" {
		t.Fatalf("source pointer changed after failure: %+v", fixture.store.resource)
	}
	old, exists := fixture.engine.containers["old-container"]
	if !exists || old.State != "running" {
		t.Fatalf("old runtime = %+v, %v", old, exists)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "new-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate volume survived: %v", err)
	}
}

func TestRedisMaintenanceRejectsNewUIDataMutations(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t, nil)
	if !fixture.controller.beginMaintenance("redis-id") {
		t.Fatal("begin maintenance failed")
	}
	defer fixture.controller.endMaintenance("redis-id")
	if _, err := fixture.controller.Mutate(context.Background(), "redis-id", Mutation{}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("mutation during maintenance = %v", err)
	}
}
