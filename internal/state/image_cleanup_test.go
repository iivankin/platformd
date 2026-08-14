package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestImageCleanupStandardKeepsSevenLatestRevisionsPerKind(t *testing.T) {
	ctx := context.Background()
	store := openImageCleanupStore(t, ctx)
	defer store.Close()

	insertImageCleanupRevision(t, store, "production-active", "production", "active", 100, 0, 0)
	for index, createdAt := range []int64{90, 80, 70, 60, 50, 40} {
		id := fmt.Sprintf("production-retired-%d", index)
		insertImageCleanupRevision(t, store, id, "production", "retired", createdAt, createdAt, 0)
	}
	insertImageCleanupRevision(t, store, "production-old", "production", "retired", 30, 30, 0)

	insertImageCleanupRevision(t, store, "preview-active", "preview", "active", 100, 0, 20)
	for index, createdAt := range []int64{90, 80, 70, 60, 50, 40} {
		id := fmt.Sprintf("preview-retired-%d", index)
		insertImageCleanupRevision(t, store, id, "preview", "retired", createdAt, createdAt, 20)
	}
	insertImageCleanupRevision(t, store, "preview-old", "preview", "retired", 30, 30, 20)
	insertImageCleanupRevision(t, store, "production-failed", "production", "failed", 110, 0, 0)
	insertImageCleanupUpload(t, store, "upload-expired", "succeeded", 90)
	insertImageCleanupUpload(t, store, "upload-current", "succeeded", 110)

	files, err := store.DeleteImageCleanupCandidates(ctx, ImageCleanupStandard, 100, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertImageCleanupPaths(t, files.ArchivePaths,
		"/archives/preview-old", "/archives/production-failed", "/archives/production-old",
	)
	assertImageCleanupPaths(t, files.UploadPaths, "/uploads/upload-expired")
	assertImageCleanupRevisionIDs(t, store,
		"preview-active",
		"preview-retired-0", "preview-retired-1", "preview-retired-2",
		"preview-retired-3", "preview-retired-4", "preview-retired-5",
		"production-active",
		"production-retired-0", "production-retired-1", "production-retired-2",
		"production-retired-3", "production-retired-4", "production-retired-5",
	)
}

func TestImageCleanupPressurePreservesRollbackAndRunningBackup(t *testing.T) {
	ctx := context.Background()
	store := openImageCleanupStore(t, ctx)
	defer store.Close()

	insertImageCleanupRevision(t, store, "production-active", "production", "active", 50, 0, 0)
	insertImageCleanupRevision(t, store, "production-previous", "production", "retired", 40, 40, 0)
	insertImageCleanupRevision(t, store, "production-old", "production", "retired", 30, 30, 0)
	insertImageCleanupRevision(t, store, "production-backing-up", "production", "retired", 20, 20, 0)
	insertImageCleanupRevision(t, store, "preview-previous", "preview", "retired", 40, 40, 200)
	insertImageCleanupRevision(t, store, "preview-old", "preview", "retired", 30, 30, 200)
	insertImageCleanupRevision(t, store, "preview-active", "preview", "active", 60, 0, 200)
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO backups(id, target_id, resource_kind, resource_id, status, started_at)
VALUES ('backup-running', 'target', 'image', 'production-backing-up', 'running', 1)`); err != nil {
		t.Fatal(err)
	}

	critical, err := store.DeleteImageCleanupCandidates(ctx, ImageCleanupCritical, 100, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertImageCleanupPaths(t, critical.ArchivePaths, "/archives/preview-old", "/archives/production-old")
	assertImageCleanupRevisionIDs(t, store,
		"preview-active", "preview-previous", "production-active", "production-backing-up", "production-previous",
	)

	emergency, err := store.DeleteImageCleanupCandidates(ctx, ImageCleanupEmergency, 100, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertImageCleanupPaths(t, emergency.ArchivePaths, "/archives/preview-previous", "/archives/production-previous")
	assertImageCleanupRevisionIDs(t, store, "preview-active", "production-active", "production-backing-up")
}

func openImageCleanupStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, enabled, created_at, updated_at)
VALUES ('service', 'project', 'service',
        '{"type":"docker_image_upload","dockerUpload":{"repository":"acme/backend","branch":"main","workflows":[]}}',
        '{}', 1, 1, 1)`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func insertImageCleanupRevision(
	t *testing.T,
	store *Store,
	id string,
	kind string,
	status string,
	createdAt int64,
	retiredAt int64,
	expiresAt int64,
) {
	t.Helper()
	var retired any
	if retiredAt > 0 {
		retired = retiredAt
	}
	var expires any
	if expiresAt > 0 {
		expires = expiresAt
	}
	if _, err := store.database.ExecContext(context.Background(), `
INSERT INTO service_image_revisions(
 id, service_id, tag, kind, archive_path, archive_sha256, oidc_metadata_json,
 status, created_at, retired_at, expires_at
) VALUES (?, 'service', ?, ?, ?, 'sha256', '{}', ?, ?, ?, ?)`,
		id, id, kind, "/archives/"+id, status, createdAt, retired, expires); err != nil {
		t.Fatal(err)
	}
}

func insertImageCleanupUpload(t *testing.T, store *Store, id string, status string, expiresAt int64) {
	t.Helper()
	if _, err := store.database.ExecContext(context.Background(), `
INSERT INTO service_image_uploads(
 id, service_id, tag, expected_length, received_length, expected_sha256,
 temporary_path, oidc_metadata_json, status, created_at, updated_at, expires_at
) VALUES (?, 'service', ?, 1, 1, 'sha256', ?, '{}', ?, 1, 1, ?)`,
		id, id, "/uploads/"+id, status, expiresAt); err != nil {
		t.Fatal(err)
	}
}

func assertImageCleanupPaths(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("cleanup paths = %v, want %v", actual, expected)
	}
}

func assertImageCleanupRevisionIDs(t *testing.T, store *Store, expected ...string) {
	t.Helper()
	rows, err := store.database.QueryContext(context.Background(), `SELECT id FROM service_image_revisions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("remaining revision IDs = %v, want %v", actual, expected)
	}
}
