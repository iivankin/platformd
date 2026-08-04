package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/bucketname"
	"github.com/iivankin/platformd/internal/corsorigin"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrBadDigest           = errors.New("object payload digest does not match x-amz-content-sha256")
	ErrInvalidInput        = errors.New("invalid object store input")
	ErrMetadataMaintenance = errors.New("object store metadata is in maintenance")
	ErrObjectNotFound      = errors.New("object not found")
	ErrPreconditionFailed  = errors.New("object precondition failed")
)

type Repository interface {
	CreateObjectStore(context.Context, state.CreateObjectStore) (state.ObjectStore, state.S3Credential, error)
	ObjectStore(context.Context, string) (state.ObjectStore, error)
	ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error)
	ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error)
	UpdateObjectStorePortForward(context.Context, state.UpdateObjectStorePortForwardInput) (state.ObjectStore, error)
	UpdateObjectStorePublicAccess(context.Context, state.UpdateObjectStorePublicAccessInput) (state.ObjectStore, error)
	S3CredentialsByObjectStore(context.Context, string) ([]state.S3Credential, error)
	RecordObjectStoreRestore(context.Context, state.RecordObjectStoreRestore) error
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type CreateInput struct {
	ProjectID            string
	Name                 string
	BucketName           string
	PublicHostname       string
	CORSOrigins          []string
	CredentialName       string
	CredentialPermission string
	BackupPolicy         state.InitialBackupPolicy
	Credentials          *InitialCredentials
	Actor                Actor
}

type CreateResult struct {
	Store      state.ObjectStore
	Credential state.S3Credential
	AccessKey  string
	Secret     string
	RequestID  string
}

type StoreDetails struct {
	Store      state.ObjectStore
	Credential state.S3Credential
	AccessKey  string
	Secret     string
}

type Application struct {
	repository Repository
	storage    Storage
	master     cryptobox.MasterKey
	random     io.Reader
	now        func() time.Time
	metadataMu sync.Mutex
	metadata   map[string]*metadataAdmission
	requests   map[string]*requestAdmission
	backups    map[string]bool
}

type metadataAdmission struct {
	active        int
	blocked       bool
	rejectBlocked bool
	changed       chan struct{}
}

type requestAdmission struct {
	active  int
	blocked bool
	changed chan struct{}
}

func NewApplication(repository Repository, storage Storage, master cryptobox.MasterKey, random io.Reader, now func() time.Time) (*Application, error) {
	if repository == nil || storage == nil {
		return nil, errors.New("object store application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Application{
		repository: repository, storage: storage, master: master, random: random, now: now,
		metadata: make(map[string]*metadataAdmission), requests: make(map[string]*requestAdmission),
		backups: make(map[string]bool),
	}, nil
}

func (application *Application) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if input.ProjectID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") {
		return CreateResult{}, fmt.Errorf("%w: create identity is incomplete", ErrInvalidInput)
	}
	if input.Actor.Kind == "access" && input.Actor.Email == "" {
		return CreateResult{}, fmt.Errorf("%w: Access email is required", ErrInvalidInput)
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := bucketname.Validate(input.BucketName); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		input.PublicHostname = hostname
	}
	var err error
	input.CORSOrigins, err = corsorigin.NormalizeAll(input.CORSOrigins)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.CredentialName == "" {
		input.CredentialName = "default"
	}
	if input.CredentialPermission == "" {
		input.CredentialPermission = "read_write"
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(4)
	if err != nil {
		return CreateResult{}, err
	}
	credentialID := identifiers[1]
	var accessKey, secret string
	if input.Credentials == nil {
		accessKey, err = AccessKeyID(credentialID)
		if err != nil {
			return CreateResult{}, err
		}
		secret, err = GenerateSecret(application.random)
		if err != nil {
			return CreateResult{}, err
		}
	} else {
		accessKey = input.Credentials.AccessKey
		secret = input.Credentials.Secret
		credentialID, err = CredentialID(accessKey)
		if err == nil && !validSecret(secret) {
			err = errors.New("S3 credential secret is invalid")
		}
		if err != nil {
			return CreateResult{}, fmt.Errorf("%w: initial object storage credentials are invalid: %v", ErrInvalidInput, err)
		}
	}
	encrypted, err := SealSecret(application.master, identifiers[0], credentialID, secret)
	if err != nil {
		return CreateResult{}, err
	}
	if err := application.storage.EnsureBucket(ctx, identifiers[0]); err != nil {
		return CreateResult{}, fmt.Errorf("initialize object storage bucket: %w", err)
	}
	created, credential, err := application.repository.CreateObjectStore(ctx, state.CreateObjectStore{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name, BucketName: input.BucketName,
		PublicHostname: input.PublicHostname, CORSOrigins: input.CORSOrigins,
		CredentialID: credentialID, CredentialName: input.CredentialName,
		CredentialPermission: input.CredentialPermission, CredentialSecret: encrypted,
		BackupPolicy: input.BackupPolicy,
		AuditEventID: identifiers[2], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[3], CreatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		_ = application.storage.DeleteBucket(ctx, identifiers[0])
		return CreateResult{}, err
	}
	return CreateResult{Store: created, Credential: credential, AccessKey: accessKey, Secret: secret, RequestID: identifiers[3]}, nil
}

func (application *Application) Store(ctx context.Context, projectID, storeID string) (state.ObjectStore, error) {
	return application.repository.ObjectStoreInProject(ctx, projectID, storeID)
}

func (application *Application) UpdatePortForward(
	ctx context.Context,
	projectID, storeID string,
	portForward *serviceconfig.PortForward,
	expectedUpdatedAt int64,
) (state.ObjectStore, error) {
	normalized, err := serviceconfig.NormalizePortForward(portForward)
	if err != nil {
		return state.ObjectStore{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return application.repository.UpdateObjectStorePortForward(ctx, state.UpdateObjectStorePortForwardInput{
		ID: storeID, ProjectID: projectID, PortForward: normalized,
		ExpectedUpdatedMillis: expectedUpdatedAt, UpdatedAtMillis: application.now().UnixMilli(),
	})
}

func (application *Application) UpdatePublicAccess(
	ctx context.Context,
	projectID, storeID string,
	publicHostname string,
	corsOrigins []string,
	expectedUpdatedAt int64,
) (state.ObjectStore, error) {
	if publicHostname != "" {
		hostname, err := publichostname.Normalize(publicHostname)
		if err != nil {
			return state.ObjectStore{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		publicHostname = hostname
	}
	normalizedCORS, err := corsorigin.NormalizeAll(corsOrigins)
	if err != nil {
		return state.ObjectStore{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return application.repository.UpdateObjectStorePublicAccess(ctx, state.UpdateObjectStorePublicAccessInput{
		ID: storeID, ProjectID: projectID, PublicHostname: publicHostname, CORSOrigins: normalizedCORS,
		ExpectedUpdatedMillis: expectedUpdatedAt, UpdatedAtMillis: application.now().UnixMilli(),
	})
}

func (application *Application) Details(ctx context.Context, projectID, storeID string) (StoreDetails, error) {
	store, err := application.repository.ObjectStoreInProject(ctx, projectID, storeID)
	if err != nil {
		return StoreDetails{}, err
	}
	credentials, err := application.repository.S3CredentialsByObjectStore(ctx, store.ID)
	if err != nil {
		return StoreDetails{}, err
	}
	if len(credentials) != 1 {
		return StoreDetails{}, errors.New("object store must have exactly one S3 credential")
	}
	credential := credentials[0]
	accessKey, err := AccessKeyID(credential.ID)
	if err != nil {
		return StoreDetails{}, err
	}
	secret, err := OpenSecret(application.master, store.ID, credential.ID, credential.SecretEncrypted)
	if err != nil {
		return StoreDetails{}, err
	}
	return StoreDetails{Store: store, Credential: credential, AccessKey: accessKey, Secret: secret}, nil
}

func (application *Application) Stores(ctx context.Context, projectID string) ([]state.ObjectStore, error) {
	return application.repository.ObjectStoresByProject(ctx, projectID)
}

func (application *Application) Stats(ctx context.Context, projectID, storeID string) (ObjectStoreStats, error) {
	store, err := application.repository.ObjectStoreInProject(ctx, projectID, storeID)
	if err != nil {
		return ObjectStoreStats{}, err
	}
	return application.storage.Stats(ctx, store.ID)
}

func (application *Application) LargestObjects(ctx context.Context, projectID, storeID string) (LargestObjectsSearch, error) {
	store, err := application.repository.ObjectStoreInProject(ctx, projectID, storeID)
	if err != nil {
		return LargestObjectsSearch{}, err
	}
	return application.storage.LargestObjects(ctx, store.ID)
}

func (application *Application) StartLargestObjects(ctx context.Context, projectID, storeID string) (LargestObjectsSearch, error) {
	store, err := application.repository.ObjectStoreInProject(ctx, projectID, storeID)
	if err != nil {
		return LargestObjectsSearch{}, err
	}
	release, err := application.beginRequest(store.ID)
	if err != nil {
		return LargestObjectsSearch{}, err
	}
	defer release()
	return application.storage.StartLargestObjects(ctx, store.ID)
}

func (application *Application) CancelLargestObjects(ctx context.Context, projectID, storeID string) (LargestObjectsSearch, error) {
	store, err := application.repository.ObjectStoreInProject(ctx, projectID, storeID)
	if err != nil {
		return LargestObjectsSearch{}, err
	}
	return application.storage.CancelLargestObjects(ctx, store.ID)
}

func (application *Application) DeleteStoreData(ctx context.Context, storeID string) error {
	if storeID == "" {
		return ErrInvalidInput
	}
	releaseExclusion, err := application.beginBackupExclusion(storeID)
	if err != nil {
		return err
	}
	defer releaseExclusion()
	releaseDataPlane, err := application.beginDataPlaneRestore(ctx, storeID)
	if err != nil {
		return err
	}
	defer func() { _ = releaseDataPlane() }()
	releaseRequests, err := application.blockRequestsForRestore(ctx, storeID)
	if err != nil {
		return err
	}
	defer releaseRequests()
	return application.storage.DeleteBucket(ctx, storeID)
}

func (application *Application) Put(ctx context.Context, input PutInput) (ObjectMetadata, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil || input.Body == nil ||
		(input.BodySizeKnown && (input.BodySize < 0 || input.BodySize > MaximumObjectSize)) {
		return ObjectMetadata{}, fmt.Errorf("%w: invalid object key or body", ErrInvalidInput)
	}
	finishMutation, err := application.beginMetadataMutation(ctx, input.StoreID)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer finishMutation()
	return application.storage.Put(ctx, input)
}

func (application *Application) Object(ctx context.Context, storeID, objectKey string) (Object, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return Object{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	release, err := application.beginRequest(storeID)
	if err != nil {
		return Object{}, err
	}
	defer release()
	metadata, err := application.storage.Object(ctx, storeID, objectKey)
	if err != nil {
		return Object{}, err
	}
	return Object{Metadata: metadata}, nil
}

func (application *Application) ReadRange(ctx context.Context, object Object, offset, length int64, output io.Writer) error {
	release, err := application.beginRequest(object.Metadata.ObjectStoreID)
	if err != nil {
		return err
	}
	defer release()
	return application.storage.ReadRange(ctx, object.Metadata.ObjectStoreID, object.Metadata.ObjectKey, offset, length, output)
}

func (application *Application) List(ctx context.Context, storeID, prefix, after string, limit int) ([]ObjectMetadata, bool, error) {
	release, err := application.beginRequest(storeID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	entries, more, err := application.storage.ListEntries(ctx, storeID, prefix, "", after, limit)
	objects := make([]ObjectMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.Object != nil {
			objects = append(objects, *entry.Object)
		}
	}
	return objects, more, err
}

func (application *Application) ListEntries(ctx context.Context, storeID, prefix, delimiter, after string, limit int) ([]ObjectListEntry, bool, error) {
	release, err := application.beginRequest(storeID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	if delimiter == "" {
		return application.storage.ListEntries(ctx, storeID, prefix, "", after, limit)
	}
	if len(delimiter) > 1024 || !utf8.ValidString(delimiter) || strings.ContainsRune(delimiter, 0) {
		return nil, false, fmt.Errorf("%w: invalid list delimiter", ErrInvalidInput)
	}
	return application.storage.ListEntries(ctx, storeID, prefix, delimiter, after, limit)
}

func (application *Application) EncodeContinuationToken(storeID, objectKey string) (string, error) {
	if !safeComponent(storeID) || validateObjectKey(objectKey) != nil {
		return "", fmt.Errorf("%w: invalid continuation cursor", ErrInvalidInput)
	}
	key, err := deriveStoreKey(application.master, storeID)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("list:" + objectKey))
	value := append([]byte(objectKey), mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (application *Application) DecodeContinuationToken(storeID, token string) (string, error) {
	value, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(value) <= sha256.Size {
		return "", fmt.Errorf("%w: invalid continuation token", ErrInvalidInput)
	}
	objectKey := string(value[:len(value)-sha256.Size])
	if validateObjectKey(objectKey) != nil {
		return "", fmt.Errorf("%w: invalid continuation token", ErrInvalidInput)
	}
	key, err := deriveStoreKey(application.master, storeID)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("list:" + objectKey))
	if subtle.ConstantTimeCompare(value[len(value)-sha256.Size:], mac.Sum(nil)) != 1 {
		return "", fmt.Errorf("%w: invalid continuation token", ErrInvalidInput)
	}
	return objectKey, nil
}

func (application *Application) Delete(ctx context.Context, storeID, objectKey string) error {
	if err := validateObjectKey(objectKey); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	finishMutation, err := application.beginMetadataMutation(ctx, storeID)
	if err != nil {
		return err
	}
	defer finishMutation()
	return application.storage.Delete(ctx, storeID, objectKey)
}

func (application *Application) beginMetadataMutation(ctx context.Context, storeID string) (func(), error) {
	application.metadataMu.Lock()
	admission := application.metadataAdmissionLocked(storeID)
	for admission.blocked {
		if admission.rejectBlocked {
			application.metadataMu.Unlock()
			return nil, ErrMetadataMaintenance
		}
		changed := admission.changed
		application.metadataMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
		application.metadataMu.Lock()
	}
	admission.active++
	application.metadataMu.Unlock()
	return sync.OnceFunc(func() {
		application.metadataMu.Lock()
		admission.active--
		application.signalMetadataLocked(admission)
		application.metadataMu.Unlock()
	}), nil
}

func (application *Application) beginRequest(storeID string) (func(), error) {
	application.metadataMu.Lock()
	admission := application.requestAdmissionLocked(storeID)
	if admission.blocked {
		application.metadataMu.Unlock()
		return nil, ErrMetadataMaintenance
	}
	admission.active++
	application.metadataMu.Unlock()
	return sync.OnceFunc(func() {
		application.metadataMu.Lock()
		admission.active--
		application.signalRequestLocked(admission)
		application.metadataMu.Unlock()
	}), nil
}

func (application *Application) blockRequestsForRestore(ctx context.Context, storeID string) (func(), error) {
	application.metadataMu.Lock()
	admission := application.requestAdmissionLocked(storeID)
	if admission.blocked {
		application.metadataMu.Unlock()
		return nil, errors.New("object store requests are already in maintenance")
	}
	admission.blocked = true
	application.signalRequestLocked(admission)
	for admission.active != 0 {
		changed := admission.changed
		application.metadataMu.Unlock()
		select {
		case <-ctx.Done():
			application.metadataMu.Lock()
			admission.blocked = false
			application.signalRequestLocked(admission)
			application.metadataMu.Unlock()
			return nil, ctx.Err()
		case <-changed:
		}
		application.metadataMu.Lock()
	}
	application.metadataMu.Unlock()
	return sync.OnceFunc(func() {
		application.metadataMu.Lock()
		admission.blocked = false
		application.signalRequestLocked(admission)
		application.metadataMu.Unlock()
	}), nil
}

func (application *Application) blockMetadata(ctx context.Context, storeID string) (func(), error) {
	return application.blockMetadataWithMode(ctx, storeID, false)
}

func (application *Application) blockMetadataForRestore(ctx context.Context, storeID string) (func(), error) {
	return application.blockMetadataWithMode(ctx, storeID, true)
}

func (application *Application) blockMetadataWithMode(ctx context.Context, storeID string, rejectBlocked bool) (func(), error) {
	application.metadataMu.Lock()
	admission := application.metadataAdmissionLocked(storeID)
	if admission.blocked {
		application.metadataMu.Unlock()
		return nil, errors.New("object store metadata is already in maintenance")
	}
	admission.blocked = true
	admission.rejectBlocked = rejectBlocked
	application.signalMetadataLocked(admission)
	for admission.active != 0 {
		changed := admission.changed
		application.metadataMu.Unlock()
		select {
		case <-ctx.Done():
			application.metadataMu.Lock()
			admission.blocked = false
			admission.rejectBlocked = false
			application.signalMetadataLocked(admission)
			application.metadataMu.Unlock()
			return nil, ctx.Err()
		case <-changed:
		}
		application.metadataMu.Lock()
	}
	application.metadataMu.Unlock()
	return sync.OnceFunc(func() {
		application.metadataMu.Lock()
		admission.blocked = false
		admission.rejectBlocked = false
		application.signalMetadataLocked(admission)
		application.metadataMu.Unlock()
	}), nil
}

func (application *Application) beginBackupExclusion(storeID string) (func(), error) {
	application.metadataMu.Lock()
	defer application.metadataMu.Unlock()
	if application.backups[storeID] {
		return nil, errors.New("object store backup is already running")
	}
	application.backups[storeID] = true
	return sync.OnceFunc(func() {
		application.metadataMu.Lock()
		delete(application.backups, storeID)
		application.metadataMu.Unlock()
	}), nil
}

func (application *Application) metadataAdmissionLocked(storeID string) *metadataAdmission {
	admission := application.metadata[storeID]
	if admission == nil {
		admission = &metadataAdmission{changed: make(chan struct{})}
		application.metadata[storeID] = admission
	}
	return admission
}

func (application *Application) requestAdmissionLocked(storeID string) *requestAdmission {
	admission := application.requests[storeID]
	if admission == nil {
		admission = &requestAdmission{changed: make(chan struct{})}
		application.requests[storeID] = admission
	}
	return admission
}

func (application *Application) signalMetadataLocked(admission *metadataAdmission) {
	close(admission.changed)
	admission.changed = make(chan struct{})
}

func (application *Application) signalRequestLocked(admission *requestAdmission) {
	close(admission.changed)
	admission.changed = make(chan struct{})
}

func (application *Application) identifiers(count int) ([]string, error) {
	result := make([]string, count)
	for index := range result {
		value, err := id.New()
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}

func validateObjectKey(value string) error {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return errors.New("object key must be non-empty UTF-8 at most 1024 bytes without NUL")
	}
	return nil
}
