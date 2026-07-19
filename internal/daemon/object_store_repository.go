package daemon

import (
	"context"
	"sync"

	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type liveObjectStoreRepository struct {
	store        *state.Store
	runtime      *runtimeStack
	certificates *origin.Selector
	router       *ingress.Router
	publicMu     *sync.Mutex
}

func (repository *liveObjectStoreRepository) CreateObjectStore(ctx context.Context, input state.CreateObjectStore) (state.ObjectStore, state.S3Credential, error) {
	created, credential, err := func() (state.ObjectStore, state.S3Credential, error) {
		repository.publicMu.Lock()
		defer repository.publicMu.Unlock()
		if input.PublicHostname != "" {
			hostname, err := publichostname.Normalize(input.PublicHostname)
			if err != nil {
				return state.ObjectStore{}, state.S3Credential{}, err
			}
			if !repository.certificates.Covers(hostname) {
				return state.ObjectStore{}, state.S3Credential{}, state.ErrCertificateCoverage
			}
			input.PublicHostname = hostname
		}
		return repository.store.CreateObjectStore(ctx, input)
	}()
	if err != nil {
		return state.ObjectStore{}, state.S3Credential{}, err
	}
	// Desired state is authoritative. Publication is intentionally best-effort:
	// runtime startup retries it from SQLite after a daemon restart.
	if err := repository.runtime.EnableObjectStore(created); err != nil {
		repository.runtime.recordObjectStoreFailure(created.ProjectID, err)
	}
	_ = repository.reloadPublicRoutes(ctx)
	return created, credential, nil
}

func (repository *liveObjectStoreRepository) reloadPublicRoutes(ctx context.Context) error {
	if repository.router == nil {
		return nil
	}
	stores, err := repository.store.ObjectStores(ctx)
	if err != nil {
		return err
	}
	hostnames := make([]string, 0, len(stores))
	for _, objectStore := range stores {
		if objectStore.PublicHostname != "" {
			hostnames = append(hostnames, objectStore.PublicHostname)
		}
	}
	repository.router.ReloadObjectStores(hostnames)
	return nil
}

func (repository *liveObjectStoreRepository) ObjectStore(ctx context.Context, storeID string) (state.ObjectStore, error) {
	return repository.store.ObjectStore(ctx, storeID)
}

func (repository *liveObjectStoreRepository) ObjectStoreInProject(ctx context.Context, projectID, storeID string) (state.ObjectStore, error) {
	return repository.store.ObjectStoreInProject(ctx, projectID, storeID)
}

func (repository *liveObjectStoreRepository) ObjectStoresByProject(ctx context.Context, projectID string) ([]state.ObjectStore, error) {
	return repository.store.ObjectStoresByProject(ctx, projectID)
}

func (repository *liveObjectStoreRepository) S3Credential(ctx context.Context, credentialID string) (state.S3Credential, error) {
	return repository.store.S3Credential(ctx, credentialID)
}

func (repository *liveObjectStoreRepository) S3CredentialsByObjectStore(ctx context.Context, objectStoreID string) ([]state.S3Credential, error) {
	return repository.store.S3CredentialsByObjectStore(ctx, objectStoreID)
}

func (repository *liveObjectStoreRepository) CommitObject(ctx context.Context, input state.CommitObject) (state.ObjectMetadata, error) {
	return repository.store.CommitObject(ctx, input)
}

func (repository *liveObjectStoreRepository) Object(ctx context.Context, storeID, objectKey string) (state.ObjectMetadata, error) {
	return repository.store.Object(ctx, storeID, objectKey)
}

func (repository *liveObjectStoreRepository) ObjectPayload(ctx context.Context, storeID, payloadID string) (state.ObjectPayload, error) {
	return repository.store.ObjectPayload(ctx, storeID, payloadID)
}

func (repository *liveObjectStoreRepository) ListObjects(ctx context.Context, storeID, prefix, after string, limit int) ([]state.ObjectMetadata, bool, error) {
	return repository.store.ListObjects(ctx, storeID, prefix, after, limit)
}

func (repository *liveObjectStoreRepository) DeleteObject(ctx context.Context, storeID, objectKey string) error {
	return repository.store.DeleteObject(ctx, storeID, objectKey)
}

func (repository *liveObjectStoreRepository) CreateMultipartUpload(ctx context.Context, input state.CreateMultipartUpload) (state.MultipartUpload, error) {
	return repository.store.CreateMultipartUpload(ctx, input)
}

func (repository *liveObjectStoreRepository) MultipartUpload(ctx context.Context, storeID, uploadID, objectKey string) (state.MultipartUpload, error) {
	return repository.store.MultipartUpload(ctx, storeID, uploadID, objectKey)
}

func (repository *liveObjectStoreRepository) CommitMultipartPart(ctx context.Context, storeID, uploadID, objectKey string, part state.MultipartPart, nowMillis int64) error {
	return repository.store.CommitMultipartPart(ctx, storeID, uploadID, objectKey, part, nowMillis)
}

func (repository *liveObjectStoreRepository) MultipartPart(ctx context.Context, storeID, uploadID, objectKey string, partNumber int) (state.MultipartPart, error) {
	return repository.store.MultipartPart(ctx, storeID, uploadID, objectKey, partNumber)
}

func (repository *liveObjectStoreRepository) MultipartParts(ctx context.Context, storeID, uploadID, objectKey string, after, limit int) ([]state.MultipartPart, bool, error) {
	return repository.store.MultipartParts(ctx, storeID, uploadID, objectKey, after, limit)
}

func (repository *liveObjectStoreRepository) CompleteMultipartUpload(ctx context.Context, input state.CompleteMultipartUpload) (state.ObjectMetadata, error) {
	return repository.store.CompleteMultipartUpload(ctx, input)
}

func (repository *liveObjectStoreRepository) AbortMultipartUpload(ctx context.Context, storeID, uploadID, objectKey string) error {
	return repository.store.AbortMultipartUpload(ctx, storeID, uploadID, objectKey)
}

func (repository *liveObjectStoreRepository) ExpiredMultipartUploads(ctx context.Context, beforeMillis int64, limit int) ([]state.MultipartUpload, error) {
	return repository.store.ExpiredMultipartUploads(ctx, beforeMillis, limit)
}

func (repository *liveObjectStoreRepository) RestoreObjectStore(ctx context.Context, input state.RestoreObjectStore) error {
	return repository.store.RestoreObjectStore(ctx, input)
}
