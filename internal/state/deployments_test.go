package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestDeploymentPublicationIsAtomicAndOptimistic(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, health_timeout_seconds, enabled, created_at, updated_at)
VALUES ('service', 'project', 'api', '{"type":"public_image","autoUpdate":true,"image":{"reference":"docker.io/library/alpine:3.22"}}', '{}', 60, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "deployment", ServiceID: "service", ImageDigest: "sha256:image",
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(context.Background(), "service", "deployment", "other", 3); !errors.Is(err, ErrServiceChanged) {
		t.Fatalf("stale activation error = %v", err)
	}
	if err := store.ActivateDeployment(context.Background(), "service", "deployment", "", 3); err != nil {
		t.Fatal(err)
	}
	service, err := store.DesiredService(context.Background(), "service")
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.Deployment(context.Background(), "deployment")
	if err != nil {
		t.Fatal(err)
	}
	if service.ActiveDeploymentID != "deployment" || deployment.Status != "succeeded" || deployment.FinishedAtMillis != 3 {
		t.Fatalf("service/deployment = %+v / %+v", service, deployment)
	}
}

func TestFailedDeploymentPairBlocksAutomaticRetry(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, health_timeout_seconds, enabled, created_at, updated_at)
VALUES ('service', 'project', 'api', '{"type":"public_image","autoUpdate":true,"image":{"reference":"docker.io/library/alpine:3.22"}}', '{}', 60, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "deployment", ServiceID: "service", ImageDigest: "sha256:image",
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailDeployment(context.Background(), "deployment", "readiness_failed", "exited", 3); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.LatestFailedDeployment(context.Background(), "service", hash, "sha256:image")
	if err != nil || !blocked {
		t.Fatalf("blocked/error = %v/%v", blocked, err)
	}
}

func TestRunningDeploymentSourceCanBeFilledBeforeTerminalStatus(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, health_timeout_seconds, enabled, created_at, updated_at)
VALUES ('service', 'project', 'api', '{"type":"public_image","autoUpdate":true,"image":{"reference":"docker.io/library/alpine:3.22"}}', '{}', 60, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(context.Background(), BeginDeployment{
		ID: "deployment", ServiceID: "service", ImageReference: "localhost/platformd-build/service:commit",
		SourceRevision: "commit", ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentSource(
		context.Background(), "deployment", "sha256:image", "localhost/platformd-build/service:commit", "commit", "build it",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishDeployment(context.Background(), "deployment", "skipped", "no_changes", "already active", 3); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.Deployment(context.Background(), "deployment")
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ImageDigest != "sha256:image" || deployment.CommitMessage != "build it" ||
		deployment.Status != "skipped" || deployment.ErrorCode != "no_changes" {
		t.Fatalf("deployment = %+v", deployment)
	}
}
