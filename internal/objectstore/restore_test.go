package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestRestoreMetadataMaintenanceRejectsNewPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	release, err := application.blockMetadataForRestore(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "during-restore", Body: bytes.NewReader([]byte("payload")),
	}); !errors.Is(err, ErrMetadataMaintenance) {
		t.Fatalf("PUT during restore error = %v", err)
	}
	if _, err := application.repository.Object(ctx, storeID, "during-restore"); err == nil {
		t.Fatal("PUT became visible during restore")
	}
}

func TestRestoreSnapshotInstallsAuthenticatedPayloadsThenAtomicallyReplacesMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	original := []byte("original encrypted object")
	first, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", ContentType: "text/plain",
		Body: bytes.NewReader(original),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, attachments := captureObjectBackupForRestore(t, ctx, application, storeID)
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", ContentType: "text/plain",
		Body: bytes.NewReader([]byte("new current object")),
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a fresh host where the payload referenced by the backup is not
	// already present. Its metadata is no longer current after the overwrite.
	if err := application.payloads.Delete(storeID, first.PayloadID); err != nil {
		t.Fatal(err)
	}

	requestID, err := application.RestoreSnapshot(ctx, RestoreInput{
		StoreID: storeID, Metadata: metadata,
		OpenAttachment: attachmentOpenerForTest(attachments),
		Actor:          Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil || requestID == "" {
		t.Fatalf("restore request = %q, %v", requestID, err)
	}
	object, err := application.Object(ctx, storeID, "folder/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	var restored bytes.Buffer
	if err := application.ReadRange(ctx, object, 0, object.Metadata.Size, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), original) {
		t.Fatalf("restored object = %q", restored.Bytes())
	}
}

func TestRestoreSnapshotLeavesCurrentMetadataOnAttachmentFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "data", Body: bytes.NewReader([]byte("old")),
	}); err != nil {
		t.Fatal(err)
	}
	metadata, attachments := captureObjectBackupForRestore(t, ctx, application, storeID)
	current := []byte("current")
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "data", Body: bytes.NewReader(current),
	}); err != nil {
		t.Fatal(err)
	}
	attachments[0][len(attachments[0])-1] ^= 0xff
	if _, err := application.RestoreSnapshot(ctx, RestoreInput{
		StoreID: storeID, Metadata: metadata,
		OpenAttachment: attachmentOpenerForTest(attachments),
		Actor:          Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	}); err == nil {
		t.Fatal("corrupt object backup attachment was accepted")
	}
	object, err := application.Object(ctx, storeID, "data")
	if err != nil {
		t.Fatal(err)
	}
	var value bytes.Buffer
	if err := application.ReadRange(ctx, object, 0, object.Metadata.Size, &value); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value.Bytes(), current) {
		t.Fatalf("current object changed after failed restore: %q", value.Bytes())
	}
}

func captureObjectBackupForRestore(
	t *testing.T,
	ctx context.Context,
	application *Application,
	storeID string,
) ([]byte, [][]byte) {
	t.Helper()
	export, err := application.BackupSnapshot(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := append([]byte(nil), export.Metadata...)
	attachments := make([][]byte, len(export.AttachmentPaths))
	for index, path := range export.AttachmentPaths {
		attachments[index], err = os.ReadFile(path)
		if err != nil {
			export.Release()
			t.Fatal(err)
		}
	}
	export.Release()
	return metadata, attachments
}

func attachmentOpenerForTest(values [][]byte) BackupAttachmentOpener {
	return func(_ context.Context, attachment BackupAttachment) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(values[attachment.Index])), nil
	}
}
