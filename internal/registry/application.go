package registry

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/registryauth"
	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/state"
)

const (
	MaximumConcurrentUploads                    = 32
	MaximumConcurrentUploadsPerCredential       = 4
	MaximumUploadsPerCredential                 = 16
	MaximumTemporaryUploadBytes           int64 = 20 << 30
	RegistryUploadTTL                           = 24 * time.Hour
)

var (
	ErrAuthentication = errors.New("registry authentication failed")
	ErrDenied         = errors.New("registry operation is denied")
	ErrInvalidInput   = errors.New("invalid registry input")
	ErrUploadQuota    = errors.New("registry upload quota exceeded")
	ErrManifestQuota  = errors.New("registry manifest quota exceeded")
)

type Store interface {
	CreateRegistryRepository(context.Context, state.CreateRegistryRepository) (state.RegistryRepository, state.RegistryCredential, error)
	RegistryRepository(context.Context, string) (state.RegistryRepository, error)
	RegistryRepositoryByName(context.Context, string) (state.RegistryRepository, error)
	RegistryRepositories(context.Context) ([]state.RegistryRepository, error)
	RegistryCredential(context.Context, string) (state.RegistryCredential, error)
	RegistryCredentials(context.Context, string) ([]state.RegistryCredential, error)
	CreateRegistryCredential(context.Context, state.CreateRegistryCredential) (state.RegistryCredential, error)
	DeleteRegistryCredential(context.Context, state.DeleteRegistryCredential) ([]state.RegistryUpload, error)
	TouchRegistryCredentialLastUsed(context.Context, string, int64) error
	SetRegistryRepositoryPublicPull(context.Context, state.SetRegistryRepositoryPublicPull) (state.RegistryRepository, error)
	CreateRegistryUpload(context.Context, state.CreateRegistryUpload) (state.RegistryUpload, error)
	RegistryUpload(context.Context, string) (state.RegistryUpload, error)
	ExpiredRegistryUploads(context.Context, int64, int) ([]state.RegistryUpload, error)
	TouchRegistryUpload(context.Context, string, int64) error
	DeleteRegistryUpload(context.Context, string) error
	PutRegistryManifest(context.Context, state.PutRegistryManifest) (state.RegistryManifest, error)
	RegistryManifest(context.Context, string, string) (state.RegistryManifest, error)
	RegistryManifestExists(context.Context, string, string) (bool, error)
	RegistryManifestCount(context.Context, string) (int, error)
	RegistryTags(context.Context, string, string, int) ([]state.RegistryTag, bool, error)
	RegistryRepositoryMetadataStats(context.Context, string) (state.RegistryRepositoryMetadataStats, error)
	RegistryManifests(context.Context, string, string, int) ([]state.RegistryManifest, bool, error)
	RegistryTagsForManifest(context.Context, string, string) ([]state.RegistryTag, error)
	DeleteRegistryTag(context.Context, state.RegistryAdminMutation) (string, error)
	DeleteRegistryManifest(context.Context, state.RegistryAdminMutation) ([]string, error)
	DeleteRegistryRepository(context.Context, state.RegistryAdminMutation) error
	RecordRegistryCleanup(context.Context, state.RegistryCleanupAudit) error
}

type Publisher interface {
	RegistryTagPublished(repository, tag string)
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type CreateRepositoryInput struct {
	Name                 string
	PublicPull           bool
	CredentialName       string
	CredentialPermission string
	Actor                Actor
}

type CreateRepositoryResult struct {
	Repository state.RegistryRepository
	Credential state.RegistryCredential
	Username   string
	Secret     string
	RequestID  string
}

type Authentication struct {
	Repository state.RegistryRepository
	Credential state.RegistryCredential
}

type Application struct {
	store             Store
	payloads          *PayloadStore
	master            cryptobox.MasterKey
	publisher         Publisher
	random            io.Reader
	now               func() time.Time
	tokens            *tokenManager
	slots             chan struct{}
	temporaryBytes    *byteQuota
	credentialSlotsMu sync.Mutex
	credentialSlots   map[string]*credentialUploadSlots
	repositoryLocksMu sync.Mutex
	repositoryLocks   map[string]*uploadLock
	admissionMu       sync.Mutex
	admissions        map[string]*repositoryAdmission
	locksMu           sync.Mutex
	locks             map[string]*uploadLock
	maintenanceMu     sync.Mutex
	maintenance       map[string]string
}

type uploadLock struct {
	mutex sync.Mutex
	users int
}

type credentialUploadSlots struct {
	slots chan struct{}
	users int
}

type byteQuota struct {
	mutex sync.Mutex
	used  int64
	limit int64
}

type repositoryAdmission struct {
	active  int
	blocked bool
	changed chan struct{}
}

func NewApplication(store Store, payloads *PayloadStore, master cryptobox.MasterKey, publisher Publisher, random io.Reader, now func() time.Time) (*Application, error) {
	if store == nil || payloads == nil {
		return nil, errors.New("registry application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	temporaryBytes, err := payloads.TemporaryBytes()
	if err != nil {
		return nil, fmt.Errorf("measure registry temporary payloads: %w", err)
	}
	tokens, err := newTokenManager(master)
	if err != nil {
		return nil, fmt.Errorf("derive registry token key: %w", err)
	}
	return &Application{
		store: store, payloads: payloads, master: master, publisher: publisher,
		random: random, now: now, tokens: tokens, slots: make(chan struct{}, MaximumConcurrentUploads),
		temporaryBytes:  &byteQuota{used: temporaryBytes, limit: MaximumTemporaryUploadBytes},
		credentialSlots: make(map[string]*credentialUploadSlots),
		repositoryLocks: make(map[string]*uploadLock),
		admissions:      make(map[string]*repositoryAdmission),
		locks:           make(map[string]*uploadLock),
		maintenance:     make(map[string]string),
	}, nil
}

func (application *Application) CreateRepository(ctx context.Context, input CreateRepositoryInput) (CreateRepositoryResult, error) {
	if input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") || (input.Actor.Kind == "access" && input.Actor.Email == "") {
		return CreateRepositoryResult{}, fmt.Errorf("%w: actor is incomplete", ErrInvalidInput)
	}
	if err := registryname.ValidateRepository(input.Name); err != nil {
		return CreateRepositoryResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.CredentialName == "" {
		input.CredentialName = "default"
	}
	if input.CredentialPermission == "" {
		input.CredentialPermission = "pull_push"
	}
	now := application.now()
	identifiers, err := application.identifiers(now, 4)
	if err != nil {
		return CreateRepositoryResult{}, err
	}
	username, err := registryauth.Username(identifiers[1])
	if err != nil {
		return CreateRepositoryResult{}, err
	}
	secret, err := registryauth.GenerateSecret(application.random)
	if err != nil {
		return CreateRepositoryResult{}, err
	}
	verifier, err := registryauth.Verifier(application.master, identifiers[0], identifiers[1], secret)
	if err != nil {
		return CreateRepositoryResult{}, err
	}
	repository, credential, err := application.store.CreateRegistryRepository(ctx, state.CreateRegistryRepository{
		ID: identifiers[0], Name: input.Name, PublicPull: input.PublicPull,
		CredentialID: identifiers[1], CredentialName: input.CredentialName,
		CredentialPermission: input.CredentialPermission, CredentialSecretHMAC: verifier,
		AuditEventID: identifiers[2], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[3], CreatedAtMillis: now.UnixMilli(),
	})
	if err != nil {
		return CreateRepositoryResult{}, err
	}
	return CreateRepositoryResult{
		Repository: repository, Credential: credential, Username: username,
		Secret: secret, RequestID: identifiers[3],
	}, nil
}

func (application *Application) Repositories(ctx context.Context) ([]state.RegistryRepository, error) {
	return application.store.RegistryRepositories(ctx)
}

func (application *Application) Repository(ctx context.Context, repositoryID string) (state.RegistryRepository, error) {
	return application.store.RegistryRepository(ctx, repositoryID)
}

func (application *Application) RepositoryByName(ctx context.Context, name string) (state.RegistryRepository, error) {
	return application.store.RegistryRepositoryByName(ctx, name)
}

func (application *Application) Authenticate(ctx context.Context, repositoryName, username, secret string, write bool) (Authentication, error) {
	authentication, err := application.AuthenticateCredential(ctx, username, secret)
	if err != nil {
		return Authentication{}, err
	}
	if authentication.Repository.Name != repositoryName {
		return Authentication{}, ErrAuthentication
	}
	if write && authentication.Credential.Permission != "pull_push" {
		return Authentication{}, ErrDenied
	}
	return authentication, nil
}

func (application *Application) AuthenticateCredential(ctx context.Context, username, secret string) (Authentication, error) {
	credentialID, err := registryauth.CredentialID(username)
	if err != nil {
		return Authentication{}, ErrAuthentication
	}
	credential, err := application.store.RegistryCredential(ctx, credentialID)
	if err != nil {
		return Authentication{}, ErrAuthentication
	}
	repository, err := application.store.RegistryRepository(ctx, credential.RepositoryID)
	if err != nil || !registryauth.Verify(application.master, repository.ID, credential.ID, secret, credential.SecretHMAC) {
		return Authentication{}, ErrAuthentication
	}
	return Authentication{Repository: repository, Credential: credential}, nil
}

func (application *Application) authenticationForCredentialID(ctx context.Context, repository state.RegistryRepository, credentialID string, write bool) (Authentication, error) {
	credential, err := application.store.RegistryCredential(ctx, credentialID)
	if err != nil || credential.RepositoryID != repository.ID {
		return Authentication{}, ErrAuthentication
	}
	if write && credential.Permission != "pull_push" {
		return Authentication{}, ErrDenied
	}
	return Authentication{Repository: repository, Credential: credential}, nil
}

func (application *Application) BeginUpload(ctx context.Context, authentication Authentication) (state.RegistryUpload, error) {
	if authentication.Repository.ID == "" || authentication.Credential.ID == "" || authentication.Repository.ID != authentication.Credential.RepositoryID || authentication.Credential.Permission != "pull_push" {
		return state.RegistryUpload{}, ErrDenied
	}
	now := application.now()
	uploadID, err := id.NewWith(now, application.random)
	if err != nil {
		return state.RegistryUpload{}, err
	}
	if err := application.payloads.BeginUpload(authentication.Repository.ID, uploadID); err != nil {
		return state.RegistryUpload{}, err
	}
	upload, err := application.store.CreateRegistryUpload(ctx, state.CreateRegistryUpload{
		ID: uploadID, RepositoryID: authentication.Repository.ID,
		CredentialID: authentication.Credential.ID, CreatedAtMillis: now.UnixMilli(),
		ExpiresAtMillis:      now.Add(RegistryUploadTTL).UnixMilli(),
		MaximumForCredential: MaximumUploadsPerCredential,
	})
	if err != nil {
		_ = application.payloads.Cancel(authentication.Repository.ID, uploadID)
		if errors.Is(err, state.ErrRegistryUploadQuota) {
			return state.RegistryUpload{}, ErrUploadQuota
		}
		return state.RegistryUpload{}, err
	}
	return upload, nil
}

func (application *Application) AppendUpload(ctx context.Context, authentication Authentication, uploadID string, body io.Reader) (int64, error) {
	return application.withUpload(ctx, authentication, uploadID, func(upload state.RegistryUpload) (int64, error) {
		size, err := application.appendTemporary(ctx, upload, body)
		if err != nil {
			return 0, err
		}
		if err := application.store.TouchRegistryUpload(ctx, upload.ID, application.now().UnixMilli()); err != nil {
			return 0, err
		}
		return size, nil
	})
}

func (application *Application) UploadStatus(ctx context.Context, authentication Authentication, uploadID string) (state.RegistryUpload, int64, error) {
	upload, err := application.authorizedUpload(ctx, authentication, uploadID)
	if err != nil {
		return state.RegistryUpload{}, 0, err
	}
	size, err := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
	return upload, size, err
}

func (application *Application) FinalizeUpload(ctx context.Context, authentication Authentication, uploadID, digest string, body io.Reader) (int64, error) {
	if err := registryname.ValidateDigest(digest); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	size, err := application.withUpload(ctx, authentication, uploadID, func(upload state.RegistryUpload) (int64, error) {
		lock := application.acquireRepositoryLock(upload.RepositoryID)
		defer application.releaseRepositoryLock(upload.RepositoryID, lock)
		if body != nil {
			if _, err := application.appendTemporary(ctx, upload, body); err != nil {
				return 0, err
			}
		}
		size, err := application.payloads.Finalize(ctx, upload.RepositoryID, upload.ID, digest, nil)
		if err != nil {
			return 0, err
		}
		application.temporaryBytes.release(size)
		if err := application.store.DeleteRegistryUpload(ctx, upload.ID); err != nil {
			return 0, err
		}
		return size, nil
	})
	return size, err
}

func (application *Application) CancelUpload(ctx context.Context, authentication Authentication, uploadID string) error {
	_, err := application.withUpload(ctx, authentication, uploadID, func(upload state.RegistryUpload) (int64, error) {
		size, err := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
		if err != nil {
			return 0, err
		}
		if err := application.payloads.Cancel(upload.RepositoryID, upload.ID); err != nil {
			return 0, err
		}
		application.temporaryBytes.release(size)
		return 0, application.store.DeleteRegistryUpload(ctx, upload.ID)
	})
	return err
}

func (application *Application) CleanupExpiredUploads(ctx context.Context) (int, error) {
	const pageSize = 256
	removed := 0
	now := application.now()
	for {
		uploads, err := application.store.ExpiredRegistryUploads(ctx, now.UnixMilli(), pageSize)
		if err != nil {
			return removed, err
		}
		if len(uploads) == 0 {
			break
		}
		for _, upload := range uploads {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			lock := application.acquireUploadLock(upload.ID)
			size, sizeErr := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
			removeErr := application.payloads.Cancel(upload.RepositoryID, upload.ID)
			if removeErr == nil {
				removeErr = application.store.DeleteRegistryUpload(ctx, upload.ID)
			}
			application.releaseUploadLock(upload.ID, lock)
			if sizeErr != nil && !errors.Is(sizeErr, os.ErrNotExist) {
				return removed, sizeErr
			}
			if removeErr != nil && !errors.Is(removeErr, state.ErrRegistryUploadNotFound) {
				return removed, removeErr
			}
			if sizeErr == nil {
				application.temporaryBytes.release(size)
			}
			removed++
		}
	}
	orphans, err := application.cleanupOrphanPayloads(ctx, now)
	return removed + orphans, err
}

func (application *Application) cleanupOrphanPayloads(ctx context.Context, now time.Time) (int, error) {
	repositories, err := application.store.RegistryRepositories(ctx)
	if err != nil {
		return 0, err
	}
	authoritative := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		authoritative[repository.ID] = struct{}{}
	}
	temporary, err := application.payloads.TemporaryUploads()
	if err != nil {
		return 0, err
	}
	temporaryByRepository := make(map[string]int64)
	for _, upload := range temporary {
		temporaryByRepository[upload.RepositoryID] += upload.Size
	}
	directories, err := application.payloads.RepositoryDirectories()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, repositoryID := range directories {
		if _, exists := authoritative[repositoryID]; exists {
			continue
		}
		if err := application.payloads.DeleteRepository(repositoryID); err != nil {
			return removed, err
		}
		application.temporaryBytes.release(temporaryByRepository[repositoryID])
		removed++
	}
	for _, payload := range temporary {
		if _, exists := authoritative[payload.RepositoryID]; !exists || payload.ModifiedAt.Add(RegistryUploadTTL).After(now) {
			continue
		}
		upload, err := application.store.RegistryUpload(ctx, payload.UploadID)
		if err == nil && upload.RepositoryID == payload.RepositoryID {
			continue
		}
		if err != nil && !errors.Is(err, state.ErrRegistryUploadNotFound) {
			return removed, err
		}
		lock := application.acquireUploadLock(payload.UploadID)
		removeErr := application.payloads.Cancel(payload.RepositoryID, payload.UploadID)
		application.releaseUploadLock(payload.UploadID, lock)
		if removeErr != nil {
			return removed, removeErr
		}
		application.temporaryBytes.release(payload.Size)
		removed++
	}
	return removed, nil
}

func (application *Application) withUpload(ctx context.Context, authentication Authentication, uploadID string, action func(state.RegistryUpload) (int64, error)) (int64, error) {
	credentialSlots, err := application.acquireCredentialSlot(ctx, authentication.Credential.ID)
	if err != nil {
		return 0, err
	}
	defer application.releaseCredentialSlot(authentication.Credential.ID, credentialSlots)
	select {
	case application.slots <- struct{}{}:
		defer func() { <-application.slots }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	lock := application.acquireUploadLock(uploadID)
	defer application.releaseUploadLock(uploadID, lock)
	upload, err := application.authorizedUpload(ctx, authentication, uploadID)
	if err != nil {
		return 0, err
	}
	return action(upload)
}

func (application *Application) appendTemporary(ctx context.Context, upload state.RegistryUpload, body io.Reader) (int64, error) {
	before, err := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
	if err != nil {
		return 0, err
	}
	reader := &quotaReader{input: body, quota: application.temporaryBytes}
	size, err := application.payloads.Append(ctx, upload.RepositoryID, upload.ID, reader)
	if err != nil {
		after, sizeErr := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
		if sizeErr == nil {
			application.temporaryBytes.release(reader.reserved - max(0, after-before))
		}
		return 0, err
	}
	return size, nil
}

func (application *Application) acquireCredentialSlot(ctx context.Context, credentialID string) (*credentialUploadSlots, error) {
	application.credentialSlotsMu.Lock()
	slots := application.credentialSlots[credentialID]
	if slots == nil {
		slots = &credentialUploadSlots{slots: make(chan struct{}, MaximumConcurrentUploadsPerCredential)}
		application.credentialSlots[credentialID] = slots
	}
	slots.users++
	application.credentialSlotsMu.Unlock()
	select {
	case slots.slots <- struct{}{}:
		return slots, nil
	case <-ctx.Done():
		application.releaseCredentialSlotReference(credentialID, slots)
		return nil, ctx.Err()
	}
}

func (application *Application) releaseCredentialSlot(credentialID string, slots *credentialUploadSlots) {
	<-slots.slots
	application.releaseCredentialSlotReference(credentialID, slots)
}

func (application *Application) releaseCredentialSlotReference(credentialID string, slots *credentialUploadSlots) {
	application.credentialSlotsMu.Lock()
	slots.users--
	if slots.users == 0 {
		delete(application.credentialSlots, credentialID)
	}
	application.credentialSlotsMu.Unlock()
}

func (quota *byteQuota) reserve(maximum int) int {
	quota.mutex.Lock()
	defer quota.mutex.Unlock()
	available := quota.limit - quota.used
	if available <= 0 || maximum <= 0 {
		return 0
	}
	reserved := min(int64(maximum), available)
	quota.used += reserved
	return int(reserved)
}

func (quota *byteQuota) release(bytes int64) {
	if bytes <= 0 {
		return
	}
	quota.mutex.Lock()
	quota.used = max(0, quota.used-bytes)
	quota.mutex.Unlock()
}

type quotaReader struct {
	input    io.Reader
	quota    *byteQuota
	reserved int64
}

func (reader *quotaReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	reserved := reader.quota.reserve(len(buffer))
	if reserved == 0 {
		var probe [1]byte
		count, err := reader.input.Read(probe[:])
		if count > 0 {
			return 0, ErrUploadQuota
		}
		return 0, err
	}
	count, err := reader.input.Read(buffer[:reserved])
	reader.reserved += int64(count)
	reader.quota.release(int64(reserved - count))
	return count, err
}

func (application *Application) authorizedUpload(ctx context.Context, authentication Authentication, uploadID string) (state.RegistryUpload, error) {
	upload, err := application.store.RegistryUpload(ctx, uploadID)
	if err != nil {
		return state.RegistryUpload{}, err
	}
	if upload.RepositoryID != authentication.Repository.ID || upload.CredentialID != authentication.Credential.ID || authentication.Credential.Permission != "pull_push" || application.now().UnixMilli() >= upload.ExpiresAtMillis {
		return state.RegistryUpload{}, ErrDenied
	}
	return upload, nil
}

func (application *Application) acquireUploadLock(uploadID string) *uploadLock {
	application.locksMu.Lock()
	lock := application.locks[uploadID]
	if lock == nil {
		lock = &uploadLock{}
		application.locks[uploadID] = lock
	}
	lock.users++
	application.locksMu.Unlock()
	lock.mutex.Lock()
	return lock
}

func (application *Application) releaseUploadLock(uploadID string, lock *uploadLock) {
	lock.mutex.Unlock()
	application.locksMu.Lock()
	lock.users--
	if lock.users == 0 {
		delete(application.locks, uploadID)
	}
	application.locksMu.Unlock()
}

func (application *Application) identifiers(timestamp time.Time, count int) ([]string, error) {
	result := make([]string, count)
	for index := range result {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}
