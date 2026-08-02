package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestRestoreSnapshotReplacesPhysicalBucketFromStreamingArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	original := []byte("original plaintext object")
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", ContentType: "text/plain",
		Body: bytes.NewReader(original), BodySize: int64(len(original)), BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	export, err := application.BackupSnapshot(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(export.Reader)
	closeErr := export.Reader.Close()
	export.Release()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "folder/data.txt", Body: bytes.NewReader([]byte("new current object")),
	}); err != nil {
		t.Fatal(err)
	}
	requestID, err := application.RestoreSnapshot(ctx, RestoreInput{
		StoreID: storeID, Archive: bytes.NewReader(archive),
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
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
	if !bytes.Equal(restored.Bytes(), original) || object.Metadata.ContentType != "text/plain" {
		t.Fatalf("restored object = %q metadata=%+v", restored.Bytes(), object.Metadata)
	}
}

func TestRestoreMaintenanceRejectsS3Requests(t *testing.T) {
	t.Parallel()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	release, err := application.blockRequestsForRestore(context.Background(), storeID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := application.beginRequest(storeID); !errors.Is(err, ErrMetadataMaintenance) {
		t.Fatalf("request during restore = %v", err)
	}
}

type blockingReadStorage struct {
	Storage
	started sync.Once
	begin   chan struct{}
	release chan struct{}
}

func (storage *blockingReadStorage) ReadRange(ctx context.Context, storeID, key string, offset, length int64, output io.Writer) error {
	storage.started.Do(func() { close(storage.begin) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-storage.release:
	}
	return storage.Storage.ReadRange(ctx, storeID, key, offset, length, output)
}

func TestRestoreWaitsForAdminReadBodyToFinish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	body := []byte("active read")
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "read.txt", Body: bytes.NewReader(body),
		BodySize: int64(len(body)), BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	object, err := application.Object(ctx, storeID, "read.txt")
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockingReadStorage{
		Storage: application.storage, begin: make(chan struct{}), release: make(chan struct{}),
	}
	application.storage = blocked
	readDone := make(chan error, 1)
	go func() {
		readDone <- application.ReadRange(ctx, object, 0, object.Metadata.Size, io.Discard)
	}()
	<-blocked.begin
	restoreAcquired := make(chan func(), 1)
	go func() {
		release, err := application.blockRequestsForRestore(ctx, storeID)
		if err != nil {
			restoreAcquired <- nil
			return
		}
		restoreAcquired <- release
	}()
	select {
	case <-restoreAcquired:
		t.Fatal("restore acquired while admin read body was active")
	case <-time.After(30 * time.Millisecond):
	}
	close(blocked.release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	release := <-restoreAcquired
	if release == nil {
		t.Fatal("restore admission failed")
	}
	release()
}

func TestRestoreValidatesWholeArchiveBeforeClearingCurrentObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	application, storeID, closeStore := objectBackupFixture(t)
	defer closeStore()
	backupBody := []byte("backup object body that will be truncated")
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "backup.txt", Body: bytes.NewReader(backupBody),
		BodySize: int64(len(backupBody)), BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	export, err := application.BackupSnapshot(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	archive, readErr := io.ReadAll(export.Reader)
	closeErr := export.Reader.Close()
	export.Release()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	current := []byte("current data must survive")
	if _, err := application.Put(ctx, PutInput{
		StoreID: storeID, ObjectKey: "current.txt", Body: bytes.NewReader(current),
		BodySize: int64(len(current)), BodySizeKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	payloadOffset := bytes.Index(archive, backupBody)
	if payloadOffset < 0 {
		t.Fatal("backup payload was not found in archive")
	}
	truncated := archive[:payloadOffset+len(backupBody)/2]
	if _, err := application.RestoreSnapshot(ctx, RestoreInput{
		StoreID: storeID, Archive: bytes.NewReader(truncated),
		Actor: Actor{Kind: "system", ID: "restore-test"},
	}); err == nil {
		t.Fatal("truncated restore succeeded")
	}
	object, err := application.Object(ctx, storeID, "current.txt")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := application.ReadRange(ctx, object, 0, object.Metadata.Size, &body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body.Bytes(), current) {
		t.Fatalf("current object after rejected restore = %q", body.Bytes())
	}
}
