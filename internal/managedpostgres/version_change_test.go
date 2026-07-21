package managedpostgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
)

const postgresVersionChangeDigest = "sha256:2977b6072948ba9e096f5bed35158528c104e354f4f43d4b471e95a4360e1312"

func TestPostgresChangeVersionStreamsDumpAndSwitchesImageVolumePointer(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, nil)
	fixture.engine.image = containerengine.Image{ID: "target-image-id", Digest: postgresVersionChangeDigest}
	fixture.engine.dumpPayload = []byte("PGDMP-direct-version-transfer")
	progress := make([]string, 0)
	err := fixture.controller.ChangeVersion(context.Background(), VersionChangeInput{
		ResourceID: "postgres-id", ImageTag: "18", ImageDigest: postgresVersionChangeDigest,
		Actor:    Actor{Kind: "token", ID: "admin-token"},
		Progress: func(value string) { progress = append(progress, value) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.store.resource.ImageTag != "18" || fixture.store.resource.ImageDigest != postgresVersionChangeDigest ||
		fixture.store.resource.VolumeID != "new-volume" {
		t.Fatalf("version-changed resource = %+v", fixture.store.resource)
	}
	switchInput := fixture.store.switchInput
	if switchInput.Action != "postgres.version_change" || switchInput.ExpectedImageTag != "17" ||
		switchInput.ExpectedImageDigest != restoreTestImageDigest || switchInput.ImageTag != "18" ||
		switchInput.ImageDigest != postgresVersionChangeDigest || switchInput.ActorKind != "token" {
		t.Fatalf("version switch = %+v", switchInput)
	}
	if string(fixture.engine.execPayload) != string(fixture.engine.dumpPayload) {
		t.Fatalf("pg_restore payload = %q, want %q", fixture.engine.execPayload, fixture.engine.dumpPayload)
	}
	created := fixture.engine.created[0]
	if created.Environment["PGDATA"] != "/var/lib/postgresql/18/docker" ||
		len(created.ManagedVolumes) != 1 || created.ManagedVolumes[0].Destination != "/var/lib/postgresql" {
		t.Fatalf("PostgreSQL 18 storage profile = env %v, volumes %+v", created.Environment, created.ManagedVolumes)
	}
	if len(fixture.engine.dumpRequest.Command) == 0 || fixture.engine.dumpRequest.Command[0] != "pg_dump" ||
		fixture.engine.dumpRequest.Environment["PGPASSWORD"] != "owner-password" {
		t.Fatalf("pg_dump request = %+v", fixture.engine.dumpRequest)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "old-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old volume survived: %v", err)
	}
	if fixture.maintenance.projectID != "project-id" || fixture.maintenance.address.String() != "10.90.0.4" ||
		fixture.maintenance.port != Port || !fixture.maintenance.released {
		t.Fatalf("maintenance = %+v", fixture.maintenance)
	}
	wantProgress := []string{
		"resolving_target_image", "withdrawing_endpoint", "draining_clients", "creating_target",
		"transferring_dump", "validating_target", "switching_active_pointer", "complete",
	}
	if !reflect.DeepEqual(progress, wantProgress) {
		t.Fatalf("progress = %v, want %v", progress, wantProgress)
	}
}

func TestPostgresChangeVersionRestoresOldRuntimeWhenPointerSwitchFails(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, errors.New("forced switch failure"))
	fixture.engine.image = containerengine.Image{ID: "target-image-id", Digest: postgresVersionChangeDigest}
	fixture.engine.dumpPayload = []byte("PGDMP-direct-version-transfer")
	err := fixture.controller.ChangeVersion(context.Background(), VersionChangeInput{
		ResourceID: "postgres-id", ImageTag: "18", ImageDigest: postgresVersionChangeDigest,
		Actor: Actor{Kind: "access", ID: "user", Email: "user@example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "forced switch failure") {
		t.Fatalf("version change error = %v", err)
	}
	if fixture.store.resource.ImageTag != "17" || fixture.store.resource.VolumeID != "old-volume" {
		t.Fatalf("source pointer changed after failure: %+v", fixture.store.resource)
	}
	old, exists := fixture.engine.containers["old-container"]
	if !exists || old.State != "running" {
		t.Fatalf("old runtime = %+v, %v", old, exists)
	}
	if _, err := os.Stat(filepath.Join(fixture.volumeRoot, "project-id", "new-volume")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate volume survived: %v", err)
	}
	if !fixture.maintenance.released {
		t.Fatal("failed version change left the firewall maintenance block active")
	}
	wantEvents := []string{"withdraw:postgres-id", "publish:old-container"}
	if !reflect.DeepEqual(fixture.publisher.events, wantEvents) {
		t.Fatalf("publication events = %v, want %v", fixture.publisher.events, wantEvents)
	}
}

func TestPostgresMaintenanceRejectsNewUIQueries(t *testing.T) {
	t.Parallel()
	fixture := newPostgresRestoreFixture(t, nil)
	if !fixture.controller.beginMaintenance("postgres-id") {
		t.Fatal("begin maintenance failed")
	}
	defer fixture.controller.endMaintenance("postgres-id")
	if _, err := fixture.controller.Query(context.Background(), "postgres-id", "SELECT 1"); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("query during maintenance = %v", err)
	}
}
