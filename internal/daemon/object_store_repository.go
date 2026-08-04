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
	if err := repository.runtime.EnableObjectStore(ctx, created); err != nil {
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

func (repository *liveObjectStoreRepository) UpdateObjectStorePortForward(
	ctx context.Context,
	input state.UpdateObjectStorePortForwardInput,
) (state.ObjectStore, error) {
	return repository.store.UpdateObjectStorePortForward(ctx, input)
}

func (repository *liveObjectStoreRepository) UpdateObjectStorePublicAccess(
	ctx context.Context,
	input state.UpdateObjectStorePublicAccessInput,
) (state.ObjectStore, error) {
	updated, err := func() (state.ObjectStore, error) {
		repository.publicMu.Lock()
		defer repository.publicMu.Unlock()
		if input.PublicHostname != "" {
			hostname, err := publichostname.Normalize(input.PublicHostname)
			if err != nil {
				return state.ObjectStore{}, err
			}
			if !repository.certificates.Covers(hostname) {
				return state.ObjectStore{}, state.ErrCertificateCoverage
			}
			input.PublicHostname = hostname
		}
		return repository.store.UpdateObjectStorePublicAccess(ctx, input)
	}()
	if err != nil {
		return state.ObjectStore{}, err
	}
	if err := repository.runtime.EnableObjectStore(ctx, updated); err != nil {
		repository.runtime.recordObjectStoreFailure(updated.ProjectID, err)
	}
	_ = repository.reloadPublicRoutes(ctx)
	return updated, nil
}

func (repository *liveObjectStoreRepository) S3CredentialsByObjectStore(ctx context.Context, objectStoreID string) ([]state.S3Credential, error) {
	return repository.store.S3CredentialsByObjectStore(ctx, objectStoreID)
}

func (repository *liveObjectStoreRepository) RecordObjectStoreRestore(ctx context.Context, input state.RecordObjectStoreRestore) error {
	return repository.store.RecordObjectStoreRestore(ctx, input)
}
