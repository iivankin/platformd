package objectstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestCleanupExpiredMultipartRemovesMetadataAndEncryptedParts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "objects")
	master := cryptobox.MasterKey{1, 2, 3}
	payloads, err := NewPayloadStore(root, master, bytes.NewReader(bytes.Repeat([]byte{9}, 2048)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_720_000_000_000)
	application, err := NewApplication(database, payloads, master, bytes.NewReader(bytes.Repeat([]byte{4}, 2048)), func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.Create(ctx, CreateInput{
		ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := application.CreateMultipart(ctx, created.Store.ID, "archive.bin", "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.UploadPart(ctx, created.Store.ID, upload.Upload.ID, "archive.bin", 1, "", bytes.NewReader([]byte("part"))); err != nil {
		t.Fatal(err)
	}
	if cleaned, err := application.CleanupExpiredMultipart(ctx, 10); err != nil || cleaned != 0 {
		t.Fatalf("cleanup before expiry = %d, %v", cleaned, err)
	}
	now = now.Add(MultipartUploadTTL)
	if cleaned, err := application.CleanupExpiredMultipart(ctx, 10); err != nil || cleaned != 1 {
		t.Fatalf("cleanup after expiry = %d, %v", cleaned, err)
	}
	if _, err := database.MultipartUpload(ctx, created.Store.ID, upload.Upload.ID, "archive.bin"); !errors.Is(err, state.ErrMultipartUploadNotFound) {
		t.Fatalf("expired multipart metadata remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, created.Store.ID, "multipart", upload.Upload.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired multipart payload remains: %v", err)
	}
}
