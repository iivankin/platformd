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

func TestBeginImageUploadSupersedesChunksButNotProcessingUpload(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	begin := func(id, path string, created int64) ([]string, error) {
		return store.BeginImageUpload(ctx, BeginImageUploadInput{
			ID: id, ServiceID: "service", Tag: "latest", ExpectedLength: 10,
			ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			TemporaryPath:  path, CreatedAtMillis: created, ExpiresAtMillis: created + 100,
		})
	}
	if _, err := begin("upload-identifier-one", "/tmp/upload-one", 2); err != nil {
		t.Fatal(err)
	}
	paths, err := begin("upload-identifier-two", "/tmp/upload-two", 3)
	if err != nil || len(paths) != 1 || paths[0] != "/tmp/upload-one" {
		t.Fatalf("superseded paths = %v, error = %v", paths, err)
	}
	first, err := store.ImageUpload(ctx, "upload-identifier-one", "service")
	if err != nil || first.Status != "superseded" {
		t.Fatalf("first upload = %+v, error = %v", first, err)
	}
	if err := store.SetImageUploadStatus(ctx, "upload-identifier-two", "uploading", "importing", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := begin("upload-identifier-three", "/tmp/upload-three", 5); !errors.Is(err, ErrImageUploadChanged) {
		t.Fatalf("processing upload replacement error = %v", err)
	}
	second, err := store.ImageUpload(ctx, "upload-identifier-two", "service")
	if err != nil || second.Status != "importing" {
		t.Fatalf("processing upload = %+v, error = %v", second, err)
	}
}

func TestMarkInterruptedFailsImportingImageRevisions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImageUpload(ctx, BeginImageUploadInput{
		ID: "upload-identifier-one", ServiceID: "service", Tag: "latest", ExpectedLength: 1,
		ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TemporaryPath:  "/uploads/upload", CreatedAtMillis: 2, ExpiresAtMillis: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceImageUpload(ctx, "upload-identifier-one", 0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImageRevision(ctx, CreateImageRevisionInput{
		ID: "revision", UploadID: "upload-identifier-one", ServiceID: "service", Tag: "latest",
		Kind: "production", ArchivePath: "/images/revision.oci",
		ArchiveSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterrupted(ctx, 99); err != nil {
		t.Fatal(err)
	}
	upload, err := store.ImageUpload(ctx, "upload-identifier-one", "service")
	if err != nil || upload.Status != "failed" || upload.ErrorCode != "daemon_restarted" {
		t.Fatalf("upload after interrupt = %+v, error = %v", upload, err)
	}
	revision, err := store.ImageRevision(ctx, "revision")
	if err != nil || revision.Status != "failed" || revision.RetiredAtMillis != 99 {
		t.Fatalf("revision after interrupt = %+v, error = %v", revision, err)
	}
}

func TestMarkInterruptedRetiresImportedImageRevisions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImageUpload(ctx, BeginImageUploadInput{
		ID: "upload-identifier-one", ServiceID: "service", Tag: "latest", ExpectedLength: 1,
		ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TemporaryPath:  "/uploads/upload", CreatedAtMillis: 2, ExpiresAtMillis: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceImageUpload(ctx, "upload-identifier-one", 0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImageRevision(ctx, CreateImageRevisionInput{
		ID: "revision", UploadID: "upload-identifier-one", ServiceID: "service", Tag: "latest",
		Kind: "production", ArchivePath: "/images/revision.oci",
		ArchiveSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageRevisionImported(ctx, "revision", "upload-identifier-one", "sha256:image", "", 5); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterrupted(ctx, 99); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ImageRevision(ctx, "revision")
	if err != nil || revision.Status != "retired" || revision.ImageDigest != "sha256:image" {
		t.Fatalf("imported revision after interrupt = %+v, error = %v", revision, err)
	}
}

func TestCompleteImageUploadAcceptsRevisionPublishedByDeployment(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImageUpload(ctx, BeginImageUploadInput{
		ID: "upload-identifier-one", ServiceID: "service", Tag: "latest", ExpectedLength: 1,
		ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TemporaryPath:  "/uploads/upload", CreatedAtMillis: 2, ExpiresAtMillis: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceImageUpload(ctx, "upload-identifier-one", 0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImageRevision(ctx, CreateImageRevisionInput{
		ID: "revision", UploadID: "upload-identifier-one", ServiceID: "service", Tag: "latest",
		Kind: "production", ArchivePath: "/archives/revision", ArchiveSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageRevisionImported(ctx, "revision", "upload-identifier-one", "sha256:image", "", 5); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, configHash, err := serviceconfig.Canonical(serviceconfig.Snapshot{
		Source: servicesource.Source{
			Type: servicesource.DockerImageUpload,
			DockerUpload: &servicesource.DockerUpload{
				Repository: "acme/backend", Branch: "main",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDeployment(ctx, BeginDeployment{
		ID: "deployment", ServiceID: "service", ImageDigest: "sha256:image",
		ImageReference: "oci-archive:/archives/revision", ImageRevisionID: "revision",
		ConfigHash: configHash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(ctx, "service", "deployment", "", 7); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteImageUpload(ctx, "upload-identifier-one", "revision", "", 8, 208, 208); err != nil {
		t.Fatal(err)
	}
	upload, err := store.ImageUpload(ctx, "upload-identifier-one", "service")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.ImageRevision(ctx, "revision")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Status != "succeeded" || revision.Status != "active" || revision.DeploymentID != "deployment" {
		t.Fatalf("completed upload = %+v, revision = %+v", upload, revision)
	}
}

func TestFailImageUploadRetiresImportedRevision(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service', '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}', '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginImageUpload(ctx, BeginImageUploadInput{
		ID: "upload-identifier-one", ServiceID: "service", Tag: "latest", ExpectedLength: 1,
		ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TemporaryPath:  "/uploads/upload", CreatedAtMillis: 2, ExpiresAtMillis: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceImageUpload(ctx, "upload-identifier-one", 0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateImageRevision(ctx, CreateImageRevisionInput{
		ID: "revision", UploadID: "upload-identifier-one", ServiceID: "service", Tag: "latest",
		Kind: "production", ArchivePath: "/images/revision.oci",
		ArchiveSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageRevisionImported(ctx, "revision", "upload-identifier-one", "sha256:image", "", 5); err != nil {
		t.Fatal(err)
	}
	if err := store.FailImageUpload(ctx, "upload-identifier-one", "deployment_failed", "container stopped", 6, 206); err != nil {
		t.Fatal(err)
	}
	upload, err := store.ImageUpload(ctx, "upload-identifier-one", "service")
	if err != nil || upload.Status != "failed" {
		t.Fatalf("failed upload = %+v, error = %v", upload, err)
	}
	revision, err := store.ImageRevision(ctx, "revision")
	if err != nil || revision.Status != "retired" || revision.ImageDigest != "sha256:image" {
		t.Fatalf("imported revision after deploy failure = %+v, error = %v", revision, err)
	}
	reusable, err := store.LatestReusableProductionRevision(ctx, "service")
	if err != nil || reusable.ID != "revision" {
		t.Fatalf("reusable revision = %+v, error = %v", reusable, err)
	}
}
