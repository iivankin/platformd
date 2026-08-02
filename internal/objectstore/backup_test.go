package objectstore

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestBackupSnapshotStreamsOneCanonicalArchiveAndBlocksWritesUntilRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", ContentType: "text/plain",
		Body: bytes.NewReader([]byte("secret object payload")), BodySize: 21, BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	export, err := application.BackupSnapshot(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := io.ReadAll(export.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := export.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := application.Put(ctx, PutInput{
			StoreID: storeID, ObjectKey: "after-snapshot", Body: bytes.NewReader([]byte("value")),
		})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("write completed before backup release: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	export.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	archive := tar.NewReader(bytes.NewReader(archiveBytes))
	header, err := archive.Next()
	if err != nil || header.Name != "manifest.json" {
		t.Fatalf("manifest header = %+v, %v", header, err)
	}
	manifest, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot BackupSnapshot
	if err := json.Unmarshal(manifest, &snapshot); err != nil || snapshot.FormatVersion != 1 || BackupFormatVersion != 1 {
		t.Fatalf("manifest = %+v, %v", snapshot, err)
	}
	header, err = archive.Next()
	if err != nil || header.Name != backupMetadataName(0) {
		t.Fatalf("object metadata header = %+v, %v", header, err)
	}
	metadata, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	var object BackupObject
	if err := json.Unmarshal(metadata, &object); err != nil || object.Key != "folder/data.txt" {
		t.Fatalf("object metadata = %+v, %v", object, err)
	}
	header, err = archive.Next()
	if err != nil || header.Name != backupDataName(0) {
		t.Fatalf("object data header = %+v, %v", header, err)
	}
	payload, err := io.ReadAll(archive)
	if err != nil || string(payload) != "secret object payload" {
		t.Fatalf("backup payload = %q, %v", payload, err)
	}
	if _, err := archive.Next(); err != io.EOF {
		t.Fatalf("archive trailer = %v", err)
	}
}

func objectBackupFixture(t *testing.T) (*Application, string, func()) {
	t.Helper()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	application, err := NewApplication(store, newMemoryStorage(), cryptobox.MasterKey{1, 2, 3, 4}, rand.Reader, func() time.Time {
		return time.UnixMilli(1_720_000_000_000)
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	created, err := application.Create(ctx, CreateInput{
		ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return application, created.Store.ID, func() { _ = store.Close() }
}
