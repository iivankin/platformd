package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if err := store.BeginPreviewDeployment(ctx, BeginPreviewDeployment{
		ID: "preview", ServiceID: "service", PullRequestNumber: 7,
		SourceRevision: "0123456789abcdef0123456789abcdef01234567",
		Hostname:       "preview.example.com", TargetPort: 8080,
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
