package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestBackupSnapshotEnumeratesCurrentMetadataAndEncryptedChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	first, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", ContentType: "text/plain",
		Body: bytes.NewReader([]byte("secret object payload")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Put(ctx, PutInput{StoreID: storeID, ObjectKey: "empty", Body: bytes.NewReader(nil)}); err != nil {
		t.Fatal(err)
	}
	export, err := application.BackupSnapshot(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	defer export.Release()
	var snapshot BackupSnapshot
	if err := json.Unmarshal(export.Metadata, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.FormatVersion != BackupFormatVersion || snapshot.StoreID != storeID ||
		len(snapshot.Objects) != 2 || len(snapshot.Payloads) != 2 || len(snapshot.Attachments) != 1 ||
		len(export.AttachmentPaths) != 1 {
		t.Fatalf("object backup snapshot = %+v paths=%v", snapshot, export.AttachmentPaths)
	}
	attachment := snapshot.Attachments[0]
	if attachment.Index != 0 || attachment.PayloadID != first.PayloadID || attachment.Size <= 0 || len(attachment.SHA256) != 64 {
		t.Fatalf("object backup attachment = %+v", attachment)
	}
	ciphertext, err := os.ReadFile(export.AttachmentPaths[0])
	if err != nil || bytes.Contains(ciphertext, []byte("secret object payload")) {
		t.Fatalf("backup attachment is not local ciphertext: %v plaintext=%t", err, bytes.Contains(ciphertext, []byte("secret object payload")))
	}
	application.metadataMu.Lock()
	busy := application.backups[storeID]
	blocked := application.metadataAdmissionLocked(storeID).blocked
	application.metadataMu.Unlock()
	if !busy || blocked {
		t.Fatalf("backup exclusion/metadata admission = busy %t blocked %t", busy, blocked)
	}
}

func TestMetadataCommitWaitsOnlyForSnapshotEnumeration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	release, err := application.blockMetadata(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := application.Put(ctx, PutInput{
			StoreID: storeID, ObjectKey: "during-backup", Body: bytes.NewReader([]byte("payload")),
		})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("metadata commit did not wait for snapshot enumeration: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := application.repository.Object(ctx, storeID, "during-backup"); err != state.ErrObjectNotFound {
		t.Fatalf("blocked metadata became visible: %v", err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata commit did not resume")
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
	master := cryptobox.MasterKey{1, 2, 3, 4}
	payloads, err := NewPayloadStore(filepath.Join(t.TempDir(), "objects"), master, rand.Reader)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, master, rand.Reader, func() time.Time {
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
