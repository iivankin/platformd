package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
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
		SourceRevision: "commit", ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 2,
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
	if service.ActiveDeploymentID != "deployment" || service.ActiveSourceRevision != "commit" ||
		deployment.Status != "succeeded" || deployment.FinishedAtMillis != 3 {
		t.Fatalf("service/deployment = %+v / %+v", service, deployment)
	}
}

func TestDeploymentActivationMovesUploadedProductionRevision(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, health_timeout_seconds, enabled, created_at, updated_at)
VALUES ('service', 'project', 'api', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 60, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(serviceconfig.Snapshot{Source: servicesource.Source{
		Type: servicesource.DockerImageUpload,
		DockerUpload: &servicesource.DockerUpload{
			Repository: "acme/backend", Branch: "main",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	begin := func(deploymentID, revisionID, digest string, createdAt int64) {
		t.Helper()
		if err := store.BeginDeployment(ctx, BeginDeployment{
			ID: deploymentID, ServiceID: "service", ImageDigest: digest,
			ImageReference: "oci-archive:/images/" + revisionID + ".oci", ImageRevisionID: revisionID,
			ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.database.ExecContext(ctx, `
INSERT INTO service_image_revisions(
  id, service_id, tag, kind, archive_path, archive_sha256, image_digest,
  deployment_id, oidc_metadata_json, status, created_at
) VALUES (?, 'service', 'latest', 'production', ?, ?, ?, ?, '{}', 'importing', ?)`,
			revisionID, "/images/"+revisionID+".oci",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			digest, deploymentID, createdAt,
		); err != nil {
			t.Fatal(err)
		}
	}

	begin("deployment-one", "revision-one", "sha256:one", 2)
	if err := store.ActivateDeployment(ctx, "service", "deployment-one", "", 3); err != nil {
		t.Fatal(err)
	}

	begin("deployment-two", "revision-two", "sha256:two", 4)
	if err := store.ActivateDeployment(ctx, "service", "deployment-two", "deployment-one", 5); err != nil {
		t.Fatal(err)
	}

	if err := store.BeginDeployment(ctx, BeginDeployment{
		ID: "deployment-rollback", ServiceID: "service", ImageDigest: "sha256:one",
		ImageReference: "oci-archive:/images/revision-one.oci", ImageRevisionID: "revision-one",
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(ctx, "service", "deployment-rollback", "deployment-two", 7); err != nil {
		t.Fatal(err)
	}

	first, err := store.ImageRevision(ctx, "revision-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ImageRevision(ctx, "revision-two")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "active" || first.DeploymentID != "deployment-rollback" || first.RetiredAtMillis != 0 {
		t.Fatalf("reactivated revision = %+v", first)
	}
	if second.Status != "retired" || second.RetiredAtMillis != 7 {
		t.Fatalf("retired revision = %+v", second)
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

func TestDiscardDeploymentOnlyRemovesRunningNonActiveAttempt(t *testing.T) {
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
	begin := func(id string, createdAt int64) {
		t.Helper()
		if err := store.BeginDeployment(context.Background(), BeginDeployment{
			ID: id, ServiceID: "service", ImageDigest: "sha256:image",
			ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	begin("active", 2)
	if err := store.ActivateDeployment(context.Background(), "service", "active", "", 3); err != nil {
		t.Fatal(err)
	}
	if err := store.DiscardDeployment(context.Background(), "active"); !errors.Is(err, ErrServiceChanged) {
		t.Fatalf("discard active deployment error = %v", err)
	}
	begin("noop", 4)
	if err := store.DiscardDeployment(context.Background(), "noop"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.database.QueryRow("SELECT count(*) FROM deployments WHERE id = 'noop'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("discarded deployment count/error = %d/%v", count, err)
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
	if err := store.FinishDeployment(context.Background(), "deployment", "skipped", "source_checks_failed", "checks failed", 3); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.Deployment(context.Background(), "deployment")
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ImageDigest != "sha256:image" || deployment.CommitMessage != "build it" ||
		deployment.Status != "skipped" || deployment.ErrorCode != "source_checks_failed" {
		t.Fatalf("deployment = %+v", deployment)
	}
}
