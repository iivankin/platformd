package registry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTemporaryByteQuotaRollsBackRejectedChunk(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	authentication, err := fixture.application.Authenticate(
		context.Background(), fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.application.temporaryBytes.limit = 5
	upload, err := fixture.application.BeginUpload(context.Background(), authentication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.AppendUpload(context.Background(), authentication, upload.ID, bytes.NewReader([]byte("123456"))); !errors.Is(err, ErrUploadQuota) {
		t.Fatalf("oversized temporary append error = %v", err)
	}
	_, size, err := fixture.application.UploadStatus(context.Background(), authentication, upload.ID)
	if err != nil || size != 0 {
		t.Fatalf("rolled back upload size = %d, %v", size, err)
	}
	if used := fixture.application.temporaryBytes.current(); used != 0 {
		t.Fatalf("temporary quota usage after rollback = %d", used)
	}
	if _, err := fixture.application.AppendUpload(context.Background(), authentication, upload.ID, bytes.NewReader([]byte("12345"))); err != nil {
		t.Fatal(err)
	}
	if used := fixture.application.temporaryBytes.current(); used != 5 {
		t.Fatalf("temporary quota usage after append = %d", used)
	}
	digest := "sha256:5994471abb01112afcc18159f6cc74b4f511b99806da59b3caf5a9c173cacfc5"
	if _, err := fixture.application.FinalizeUpload(context.Background(), authentication, upload.ID, digest, nil); err != nil {
		t.Fatal(err)
	}
	if used := fixture.application.temporaryBytes.current(); used != 0 {
		t.Fatalf("temporary quota usage after finalize = %d", used)
	}
}

func TestIncompleteUploadQuotaIsReleasedByCancel(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	authentication, err := fixture.application.Authenticate(
		context.Background(), fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	uploads := make([]string, MaximumUploadsPerCredential)
	for index := range uploads {
		upload, err := fixture.application.BeginUpload(context.Background(), authentication)
		if err != nil {
			t.Fatalf("begin upload %d: %v", index, err)
		}
		uploads[index] = upload.ID
	}
	if _, err := fixture.application.BeginUpload(context.Background(), authentication); !errors.Is(err, ErrUploadQuota) {
		t.Fatalf("seventeenth upload error = %v", err)
	}
	if err := fixture.application.CancelUpload(context.Background(), authentication, uploads[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.BeginUpload(context.Background(), authentication); err != nil {
		t.Fatalf("begin after cancel: %v", err)
	}
}

func TestConcurrentUploadQuotaPerCredential(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	authentication, err := fixture.application.Authenticate(
		context.Background(), fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	uploads := make([]string, MaximumConcurrentUploadsPerCredential+1)
	for index := range uploads {
		upload, err := fixture.application.BeginUpload(context.Background(), authentication)
		if err != nil {
			t.Fatal(err)
		}
		uploads[index] = upload.ID
	}

	entered := make(chan struct{}, len(uploads))
	release := make(chan struct{})
	errorsChannel := make(chan error, len(uploads))
	var wait sync.WaitGroup
	for _, uploadID := range uploads {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.application.AppendUpload(context.Background(), authentication, uploadID, &gatedReader{entered: entered, release: release})
			errorsChannel <- err
		}()
	}
	for range MaximumConcurrentUploadsPerCredential {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("four concurrent credential uploads did not enter")
		}
	}
	select {
	case <-entered:
		t.Fatal("fifth upload bypassed per-credential concurrency quota")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCleanupRemovesOldOrphanPayloads(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	authentication, err := fixture.application.Authenticate(
		ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	active, err := fixture.application.BeginUpload(ctx, authentication)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		repositoryID string
		uploadID     string
	}{
		{repositoryID: fixture.private.Repository.ID, uploadID: "orphan-upload"},
		{repositoryID: "orphan-repository", uploadID: "orphan-upload"},
	} {
		if err := fixture.application.payloads.BeginUpload(candidate.repositoryID, candidate.uploadID); err != nil {
			t.Fatal(err)
		}
		path, err := fixture.application.payloads.uploadPath(candidate.repositoryID, candidate.uploadID)
		if err != nil {
			t.Fatal(err)
		}
		old := fixture.application.now().Add(-RegistryUploadTTL - time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := fixture.application.CleanupExpiredUploads(ctx)
	if err != nil || removed != 2 {
		t.Fatalf("cleanup removed %d payloads: %v", removed, err)
	}
	if _, _, err := fixture.application.UploadStatus(ctx, authentication, active.ID); err != nil {
		t.Fatalf("live upload was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.application.payloads.root, "orphan-repository")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan repository directory survived: %v", err)
	}
}

type gatedReader struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (reader *gatedReader) Read(buffer []byte) (int, error) {
	reader.once.Do(func() {
		reader.entered <- struct{}{}
		<-reader.release
	})
	return 0, io.EOF
}

func (quota *byteQuota) current() int64 {
	quota.mutex.Lock()
	defer quota.mutex.Unlock()
	return quota.used
}
