package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
)

func TestPreviewGCWaitsForCloudflareCleanup(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "project", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")}
	if _, err := store.CreateService(ctx, CreateService{
		ID: "service", ProjectID: "project", Name: "service", Enabled: true,
		Snapshot: snapshot, AuditEventID: "service-audit", ActorKind: "access",
		ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `INSERT INTO service_image_revisions(
id, service_id, tag, kind, archive_path, archive_sha256, image_digest,
oidc_metadata_json, status, created_at
) VALUES ('revision', 'service', 'preview', 'preview', '/images/revision.oci', ?, 'sha256:digest', '{}', 'importing', 3)`, strings.Repeat("a", 64))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPreviewDeployment(ctx, BeginPreviewDeployment{
		ID: "preview", ServiceID: "service", Tag: "preview", ImageRevisionID: "revision",
		Hostname: "preview.example.com", TargetPort: 8080,
		ImageDigest: "sha256:digest", ImageReference: "oci-archive:/images/revision.oci",
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 3, ExpiresAtMillis: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPreviewDNSRecords(ctx, "preview", []string{"dns-record"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivatePreviewDeployment(ctx, "preview", "", []string{"dns-record"}, 4); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ImageRevision(ctx, "revision")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != "active" || revision.PreviewID != "preview" || revision.ActivatedAtMillis != 4 {
		t.Fatalf("activated preview revision = %+v", revision)
	}
	if err := store.StopPreviewDeployment(ctx, "preview", 5); err != nil {
		t.Fatal(err)
	}

	removed, err := store.DeleteFinishedPreviewDeployments(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed preview before DNS cleanup: %#v", removed)
	}
	pending, err := store.FinishedPreviewDeploymentsWithDNS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "preview" {
		t.Fatalf("pending DNS cleanup = %#v", pending)
	}
	if err := store.ClearPreviewDNSRecords(ctx, "preview"); err != nil {
		t.Fatal(err)
	}
	removed, err = store.DeleteFinishedPreviewDeployments(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].ID != "preview" {
		t.Fatalf("removed previews = %#v", removed)
	}
	if _, err := store.PreviewDeployment(ctx, "project", "service", "preview"); !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("preview lookup after GC = %v", err)
	}
}
