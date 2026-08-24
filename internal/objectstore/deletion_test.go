package objectstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type cancelingObjectStoreRepository struct {
	*state.Store
	cancel context.CancelFunc
}

func (repository cancelingObjectStoreRepository) DeleteObjectStore(
	ctx context.Context,
	input state.DeleteResourceInput,
) (state.ObjectStore, error) {
	deleted, err := repository.Store.DeleteObjectStore(ctx, input)
	if err == nil {
		repository.cancel()
	}
	return deleted, err
}

type deletionStorage struct {
	*memoryStorage
	deleteContextErr error
}

func (storage *deletionStorage) DeleteBucket(ctx context.Context, storeID string) error {
	storage.deleteContextErr = ctx.Err()
	return storage.memoryStorage.DeleteBucket(ctx, storeID)
}

func TestDeleteResourceCleansBucketAfterRequestCancellation(t *testing.T) {
	t.Parallel()
	database, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	repository := cancelingObjectStoreRepository{Store: database, cancel: cancelRequest}
	storage := &deletionStorage{memoryStorage: newMemoryStorage()}
	application, err := NewApplication(repository, storage, cryptobox.MasterKey{1, 2, 3, 4}, nil, func() time.Time {
		return time.UnixMilli(1_720_000_000_000)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.Create(context.Background(), CreateInput{
		ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := application.DeleteResource(requestContext, DeleteInput{
		ProjectID: "project", StoreID: created.Store.ID, ExpectedUpdatedAt: created.Store.UpdatedAtMillis,
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	if storage.deleteContextErr != nil {
		t.Fatalf("bucket cleanup inherited canceled request context: %v", storage.deleteContextErr)
	}
	storage.mu.Lock()
	_, bucketExists := storage.buckets[created.Store.ID]
	storage.mu.Unlock()
	if bucketExists {
		t.Fatal("deleted object store bucket still exists")
	}
	application.metadataMu.Lock()
	_, metadataExists := application.metadata[created.Store.ID]
	_, requestsExist := application.requests[created.Store.ID]
	application.metadataMu.Unlock()
	if metadataExists || requestsExist {
		t.Fatalf("deleted object store admissions remain: metadata=%t requests=%t", metadataExists, requestsExist)
	}
	if _, err := database.ObjectStore(context.Background(), created.Store.ID); !errors.Is(err, state.ErrObjectStoreNotFound) {
		t.Fatalf("deleted object store lookup = %v", err)
	}
}
