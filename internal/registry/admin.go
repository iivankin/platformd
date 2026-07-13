package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/state"
)

var ErrRepositoryBusy = errors.New("registry repository is busy")

type RepositorySummary struct {
	Repository          state.RegistryRepository
	ManifestCount       int
	TagCount            int
	BlobCount           int
	TotalBlobBytes      int64
	ReferencedBlobBytes int64
	LastPushedAtMillis  int64
}

type ImagePlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

type Image struct {
	Digest              string
	Tags                []string
	MediaType           string
	Platforms           []ImagePlatform
	PushedAtMillis      int64
	ManifestSize        int64
	ReferencedBlobBytes int64
	BlobDigests         []string
	ManifestJSON        []byte
}

type DeleteInput struct {
	RepositoryID string
	Reference    string
	ExpectedName string
	Actor        Actor
}

type SetPublicPullInput struct {
	RepositoryID string
	PublicPull   bool
	Actor        Actor
}

func (application *Application) SetPublicPull(ctx context.Context, input SetPublicPullInput) (state.RegistryRepository, string, error) {
	if input.RepositoryID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") || (input.Actor.Kind == "access" && input.Actor.Email == "") {
		return state.RegistryRepository{}, "", fmt.Errorf("%w: repository policy actor or repository is incomplete", ErrInvalidInput)
	}
	now := application.now()
	identifiers, err := application.identifiers(now, 2)
	if err != nil {
		return state.RegistryRepository{}, "", err
	}
	repository, err := application.store.SetRegistryRepositoryPublicPull(ctx, state.SetRegistryRepositoryPublicPull{
		RepositoryID: input.RepositoryID, PublicPull: input.PublicPull,
		AuditEventID: identifiers[0], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[1], UpdatedAtMillis: now.UnixMilli(),
	})
	return repository, identifiers[1], err
}

type ManifestReferencedError struct {
	Parents []string
}

func (err *ManifestReferencedError) Error() string {
	return fmt.Sprintf("manifest is referenced by parent indexes: %v", err.Parents)
}

func (application *Application) RepositorySummaries(ctx context.Context) ([]RepositorySummary, error) {
	repositories, err := application.store.RegistryRepositories(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]RepositorySummary, 0, len(repositories))
	for _, repository := range repositories {
		summary, err := application.repositorySummary(ctx, repository)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, nil
}

func (application *Application) RepositorySummary(ctx context.Context, repositoryID string) (RepositorySummary, error) {
	repository, err := application.store.RegistryRepository(ctx, repositoryID)
	if err != nil {
		return RepositorySummary{}, err
	}
	return application.repositorySummary(ctx, repository)
}

func (application *Application) repositorySummary(ctx context.Context, repository state.RegistryRepository) (RepositorySummary, error) {
	metadata, err := application.store.RegistryRepositoryMetadataStats(ctx, repository.ID)
	if err != nil {
		return RepositorySummary{}, err
	}
	blobs, err := application.payloads.RepositoryBlobStats(repository.ID)
	if err != nil {
		return RepositorySummary{}, err
	}
	referencedBytes, err := application.referencedBlobBytes(ctx, repository.ID)
	if err != nil {
		return RepositorySummary{}, err
	}
	return RepositorySummary{
		Repository: repository, ManifestCount: metadata.ManifestCount, TagCount: metadata.TagCount,
		BlobCount: blobs.Count, TotalBlobBytes: blobs.Bytes, ReferencedBlobBytes: referencedBytes,
		LastPushedAtMillis: metadata.LastPushedAtMillis,
	}, nil
}

func (application *Application) referencedBlobBytes(ctx context.Context, repositoryID string) (int64, error) {
	referenced := make(map[string]struct{})
	after := ""
	for {
		manifests, more, err := application.store.RegistryManifests(ctx, repositoryID, after, 1000)
		if err != nil {
			return 0, err
		}
		for _, manifest := range manifests {
			if manifest.MediaType == OCIImageIndexMediaType || manifest.MediaType == DockerManifestListMediaType {
				continue
			}
			var document manifestDocument
			if err := json.Unmarshal(manifest.Body, &document); err != nil {
				return 0, err
			}
			referenced[document.Config.Digest] = struct{}{}
			for _, layer := range document.Layers {
				referenced[layer.Digest] = struct{}{}
			}
		}
		if !more || len(manifests) == 0 {
			break
		}
		after = manifests[len(manifests)-1].Digest
	}
	blobs, err := application.payloads.RepositoryBlobs(repositoryID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, blob := range blobs {
		if _, exists := referenced[blob.Digest]; exists {
			total += blob.Size
		}
	}
	return total, nil
}

func (application *Application) Images(ctx context.Context, repositoryID, afterDigest string, limit int) ([]Image, bool, error) {
	if _, err := application.store.RegistryRepository(ctx, repositoryID); err != nil {
		return nil, false, err
	}
	manifests, more, err := application.store.RegistryManifests(ctx, repositoryID, afterDigest, limit)
	if err != nil {
		return nil, false, err
	}
	result := make([]Image, 0, len(manifests))
	for _, manifest := range manifests {
		image, err := application.describeImage(ctx, manifest)
		if err != nil {
			return nil, false, err
		}
		result = append(result, image)
	}
	return result, more, nil
}

func (application *Application) Image(ctx context.Context, repositoryID, digest string) (Image, error) {
	if err := registryname.ValidateDigest(digest); err != nil {
		return Image{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	manifest, err := application.store.RegistryManifest(ctx, repositoryID, digest)
	if err != nil {
		return Image{}, err
	}
	return application.describeImage(ctx, manifest)
}

func (application *Application) describeImage(ctx context.Context, manifest state.RegistryManifest) (Image, error) {
	var document manifestDocument
	if err := json.Unmarshal(manifest.Body, &document); err != nil {
		return Image{}, fmt.Errorf("decode stored registry manifest: %w", err)
	}
	tags, err := application.store.RegistryTagsForManifest(ctx, manifest.RepositoryID, manifest.Digest)
	if err != nil {
		return Image{}, err
	}
	result := Image{
		Digest: manifest.Digest, MediaType: manifest.MediaType, PushedAtMillis: manifest.PushedAtMillis,
		ManifestSize: int64(len(manifest.Body)), ManifestJSON: slices.Clone(manifest.Body),
		Tags:        make([]string, 0, len(tags)),
		Platforms:   []ImagePlatform{},
		BlobDigests: []string{},
	}
	for _, tag := range tags {
		result.Tags = append(result.Tags, tag.Name)
	}
	if manifest.MediaType == OCIImageIndexMediaType || manifest.MediaType == DockerManifestListMediaType {
		for _, descriptor := range document.Manifests {
			if descriptor.Platform.OS != "" || descriptor.Platform.Architecture != "" {
				result.Platforms = append(result.Platforms, descriptor.Platform)
			}
		}
		return result, nil
	}
	descriptors := append([]manifestDescriptor{document.Config}, document.Layers...)
	seen := make(map[string]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		if _, exists := seen[descriptor.Digest]; exists {
			continue
		}
		seen[descriptor.Digest] = struct{}{}
		file, size, err := application.payloads.OpenBlob(manifest.RepositoryID, descriptor.Digest)
		if err != nil {
			return Image{}, err
		}
		if err := file.Close(); err != nil {
			return Image{}, err
		}
		result.BlobDigests = append(result.BlobDigests, descriptor.Digest)
		result.ReferencedBlobBytes += size
	}
	return result, nil
}

func (application *Application) DeleteTag(ctx context.Context, input DeleteInput) (string, string, error) {
	if err := validateDeleteInput(input); err != nil {
		return "", "", err
	}
	if err := registryname.ValidateTag(input.Reference); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	lock := application.acquireRepositoryLock(input.RepositoryID)
	defer application.releaseRepositoryLock(input.RepositoryID, lock)
	mutation, requestID, err := application.adminMutation(input)
	if err != nil {
		return "", "", err
	}
	digest, err := application.store.DeleteRegistryTag(ctx, mutation)
	return digest, requestID, err
}

func (application *Application) DeleteManifest(ctx context.Context, input DeleteInput) ([]string, string, error) {
	if err := validateDeleteInput(input); err != nil {
		return nil, "", err
	}
	if err := registryname.ValidateDigest(input.Reference); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	lock := application.acquireRepositoryLock(input.RepositoryID)
	defer application.releaseRepositoryLock(input.RepositoryID, lock)
	parents, err := application.manifestParents(ctx, input.RepositoryID, input.Reference)
	if err != nil {
		return nil, "", err
	}
	if len(parents) != 0 {
		return nil, "", &ManifestReferencedError{Parents: parents}
	}
	mutation, requestID, err := application.adminMutation(input)
	if err != nil {
		return nil, "", err
	}
	tags, err := application.store.DeleteRegistryManifest(ctx, mutation)
	return tags, requestID, err
}

func (application *Application) DeleteRepository(ctx context.Context, input DeleteInput) (string, error) {
	if err := validateDeleteInput(input); err != nil || input.Reference != "" || input.ExpectedName == "" {
		return "", fmt.Errorf("%w: repository deletion input is invalid", ErrInvalidInput)
	}
	repository, err := application.store.RegistryRepository(ctx, input.RepositoryID)
	if err != nil {
		return "", err
	}
	if repository.Name != input.ExpectedName {
		return "", fmt.Errorf("%w: repository confirmation name does not match", ErrInvalidInput)
	}
	releaseMaintenance, err := application.beginRepositoryMaintenance(repository.ID, "delete")
	if err != nil {
		return "", err
	}
	defer releaseMaintenance()
	finishDrain, err := application.drainRepository(ctx, repository.ID)
	if err != nil {
		return "", err
	}
	succeeded := false
	defer func() { finishDrain(succeeded) }()
	lock := application.acquireRepositoryLock(repository.ID)
	defer application.releaseRepositoryLock(repository.ID, lock)
	mutation, requestID, err := application.adminMutation(input)
	if err != nil {
		return "", err
	}
	mutation.Reference = ""
	if err := application.store.DeleteRegistryRepository(ctx, mutation); err != nil {
		return "", err
	}
	succeeded = true
	if err := application.payloads.DeleteRepository(repository.ID); err != nil {
		return requestID, fmt.Errorf("delete registry repository payloads: %w", err)
	}
	return requestID, nil
}

func (application *Application) beginRepositoryMaintenance(repositoryID, kind string) (func(), error) {
	application.maintenanceMu.Lock()
	defer application.maintenanceMu.Unlock()
	if repositoryID == "" || kind == "" || application.maintenance[repositoryID] != "" {
		return nil, ErrRepositoryBusy
	}
	application.maintenance[repositoryID] = kind
	return sync.OnceFunc(func() {
		application.maintenanceMu.Lock()
		delete(application.maintenance, repositoryID)
		application.maintenanceMu.Unlock()
	}), nil
}

func (application *Application) manifestParents(ctx context.Context, repositoryID, targetDigest string) ([]string, error) {
	const pageSize = 1000
	after := ""
	parents := make([]string, 0)
	for {
		manifests, more, err := application.store.RegistryManifests(ctx, repositoryID, after, pageSize)
		if err != nil {
			return nil, err
		}
		for _, manifest := range manifests {
			if manifest.Digest == targetDigest || (manifest.MediaType != OCIImageIndexMediaType && manifest.MediaType != DockerManifestListMediaType) {
				continue
			}
			var document manifestDocument
			if err := json.Unmarshal(manifest.Body, &document); err != nil {
				return nil, err
			}
			for _, child := range document.Manifests {
				if child.Digest == targetDigest {
					parents = append(parents, manifest.Digest)
					break
				}
			}
		}
		if !more || len(manifests) == 0 {
			return parents, nil
		}
		after = manifests[len(manifests)-1].Digest
	}
}

func validateDeleteInput(input DeleteInput) error {
	if input.RepositoryID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") || (input.Actor.Kind == "access" && input.Actor.Email == "") {
		return fmt.Errorf("%w: deletion actor or repository is incomplete", ErrInvalidInput)
	}
	return nil
}

func (application *Application) adminMutation(input DeleteInput) (state.RegistryAdminMutation, string, error) {
	now := application.now()
	identifiers, err := application.identifiers(now, 2)
	if err != nil {
		return state.RegistryAdminMutation{}, "", err
	}
	return state.RegistryAdminMutation{
		RepositoryID: input.RepositoryID, Reference: input.Reference,
		AuditEventID: identifiers[0], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[1], CreatedAtMillis: now.UnixMilli(),
	}, identifiers[1], nil
}

func (application *Application) BeginRepositoryRequest(repositoryID string) (func(), error) {
	application.admissionMu.Lock()
	admission := application.admissions[repositoryID]
	if admission == nil {
		admission = &repositoryAdmission{changed: make(chan struct{})}
		application.admissions[repositoryID] = admission
	}
	if admission.blocked {
		application.admissionMu.Unlock()
		return nil, ErrRepositoryBusy
	}
	admission.active++
	application.admissionMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			application.admissionMu.Lock()
			admission.active--
			application.signalAdmission(admission)
			application.admissionMu.Unlock()
		})
	}, nil
}

func (application *Application) drainRepository(ctx context.Context, repositoryID string) (func(bool), error) {
	application.admissionMu.Lock()
	admission := application.admissions[repositoryID]
	if admission == nil {
		admission = &repositoryAdmission{changed: make(chan struct{})}
		application.admissions[repositoryID] = admission
	}
	if admission.blocked {
		application.admissionMu.Unlock()
		return nil, ErrRepositoryBusy
	}
	admission.blocked = true
	application.signalAdmission(admission)
	for admission.active != 0 {
		changed := admission.changed
		application.admissionMu.Unlock()
		select {
		case <-ctx.Done():
			application.admissionMu.Lock()
			admission.blocked = false
			application.signalAdmission(admission)
			application.admissionMu.Unlock()
			return nil, ErrRepositoryBusy
		case <-changed:
		}
		application.admissionMu.Lock()
	}
	application.admissionMu.Unlock()
	return func(success bool) {
		if success {
			// Keep the in-memory tombstone until process exit so a request that
			// resolved metadata immediately before deletion cannot revive paths.
			return
		}
		application.admissionMu.Lock()
		admission.blocked = false
		application.signalAdmission(admission)
		application.admissionMu.Unlock()
	}, nil
}

func (application *Application) signalAdmission(admission *repositoryAdmission) {
	close(admission.changed)
	admission.changed = make(chan struct{})
}

func (application *Application) acquireRepositoryLock(repositoryID string) *uploadLock {
	application.repositoryLocksMu.Lock()
	lock := application.repositoryLocks[repositoryID]
	if lock == nil {
		lock = &uploadLock{}
		application.repositoryLocks[repositoryID] = lock
	}
	lock.users++
	application.repositoryLocksMu.Unlock()
	lock.mutex.Lock()
	return lock
}

func (application *Application) releaseRepositoryLock(repositoryID string, lock *uploadLock) {
	lock.mutex.Unlock()
	application.repositoryLocksMu.Lock()
	lock.users--
	if lock.users == 0 {
		delete(application.repositoryLocks, repositoryID)
	}
	application.repositoryLocksMu.Unlock()
}
