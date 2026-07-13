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
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrBadDigest           = errors.New("object payload digest does not match x-amz-content-sha256")
	ErrInvalidInput        = errors.New("invalid object store input")
	ErrMetadataMaintenance = errors.New("object store metadata is in maintenance")
)

type Repository interface {
	CreateObjectStore(context.Context, state.CreateObjectStore) (state.ObjectStore, state.S3Credential, error)
	ObjectStore(context.Context, string) (state.ObjectStore, error)
	ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error)
	ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error)
	S3Credential(context.Context, string) (state.S3Credential, error)
	CommitObject(context.Context, state.CommitObject) (state.ObjectMetadata, error)
	Object(context.Context, string, string) (state.ObjectMetadata, error)
	ObjectPayload(context.Context, string, string) (state.ObjectPayload, error)
	ListObjects(context.Context, string, string, string, int) ([]state.ObjectMetadata, bool, error)
	DeleteObject(context.Context, string, string) error
	CreateMultipartUpload(context.Context, state.CreateMultipartUpload) (state.MultipartUpload, error)
	MultipartUpload(context.Context, string, string, string) (state.MultipartUpload, error)
	CommitMultipartPart(context.Context, string, string, string, state.MultipartPart, int64) error
	MultipartPart(context.Context, string, string, string, int) (state.MultipartPart, error)
	MultipartParts(context.Context, string, string, string, int, int) ([]state.MultipartPart, bool, error)
	CompleteMultipartUpload(context.Context, state.CompleteMultipartUpload) (state.ObjectMetadata, error)
	AbortMultipartUpload(context.Context, string, string, string) error
	RestoreObjectStore(context.Context, state.RestoreObjectStore) error
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
	Actor                Actor
}

type CreateResult struct {
	Store      state.ObjectStore
	Credential state.S3Credential
	AccessKey  string
	Secret     string
	RequestID  string
}

type PutInput struct {
	StoreID        string
	ObjectKey      string
	ContentType    string
	ExpectedSHA256 string
	Body           io.Reader
}

type Object struct {
	Metadata state.ObjectMetadata
	Payload  PayloadInfo
}

type Credential struct {
	Store      state.ObjectStore
	Permission string
	Secret     string
}

type Application struct {
	repository Repository
	payloads   *PayloadStore
	master     cryptobox.MasterKey
	random     io.Reader
	now        func() time.Time
	metadataMu sync.Mutex
	metadata   map[string]*metadataAdmission
	backups    map[string]bool
}

type metadataAdmission struct {
	active        int
	blocked       bool
	rejectBlocked bool
	changed       chan struct{}
}

func NewApplication(repository Repository, payloads *PayloadStore, master cryptobox.MasterKey, random io.Reader, now func() time.Time) (*Application, error) {
	if repository == nil || payloads == nil {
		return nil, errors.New("object store application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Application{
		repository: repository, payloads: payloads, master: master, random: random, now: now,
		metadata: make(map[string]*metadataAdmission), backups: make(map[string]bool),
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
	identifiers, err := application.identifiers(timestamp, 4)
	if err != nil {
		return CreateResult{}, err
	}
	accessKey, err := AccessKeyID(identifiers[1])
	if err != nil {
		return CreateResult{}, err
	}
	secret, err := GenerateSecret(application.random)
	if err != nil {
		return CreateResult{}, err
	}
	encrypted, err := SealSecret(application.master, identifiers[0], identifiers[1], secret)
	if err != nil {
		return CreateResult{}, err
	}
	created, credential, err := application.repository.CreateObjectStore(ctx, state.CreateObjectStore{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name, BucketName: input.BucketName,
		PublicHostname: input.PublicHostname, CORSOrigins: input.CORSOrigins,
		CredentialID: identifiers[1], CredentialName: input.CredentialName,
		CredentialPermission: input.CredentialPermission, CredentialSecret: encrypted,
		AuditEventID: identifiers[2], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[3], CreatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Store: created, Credential: credential, AccessKey: accessKey, Secret: secret, RequestID: identifiers[3]}, nil
}

func (application *Application) Store(ctx context.Context, projectID, storeID string) (state.ObjectStore, error) {
	return application.repository.ObjectStoreInProject(ctx, projectID, storeID)
}

func (application *Application) Stores(ctx context.Context, projectID string) ([]state.ObjectStore, error) {
	return application.repository.ObjectStoresByProject(ctx, projectID)
}

func (application *Application) Credential(ctx context.Context, accessKey string) (Credential, error) {
	credentialID, err := CredentialID(accessKey)
	if err != nil {
		return Credential{}, state.ErrS3CredentialNotFound
	}
	credential, err := application.repository.S3Credential(ctx, credentialID)
	if err != nil {
		return Credential{}, err
	}
	store, err := application.repository.ObjectStore(ctx, credential.ObjectStoreID)
	if err != nil {
		return Credential{}, err
	}
	secret, err := OpenSecret(application.master, store.ID, credential.ID, credential.SecretEncrypted)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Store: store, Permission: credential.Permission, Secret: secret}, nil
}

func (application *Application) Put(ctx context.Context, input PutInput) (state.ObjectMetadata, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil || input.Body == nil {
		return state.ObjectMetadata{}, fmt.Errorf("%w: invalid object key or body", ErrInvalidInput)
	}
	payloadID, err := id.NewWith(application.now(), application.random)
	if err != nil {
		return state.ObjectMetadata{}, err
	}
	payload, err := application.payloads.Write(ctx, input.StoreID, payloadID, input.Body)
	if err != nil {
		return state.ObjectMetadata{}, err
	}
	if input.ExpectedSHA256 != "" && !strings.EqualFold(input.ExpectedSHA256, payload.PlaintextSHA256) {
		_ = application.payloads.Delete(input.StoreID, payloadID)
		return state.ObjectMetadata{}, ErrBadDigest
	}
	timestamp := application.now().UnixMilli()
	finishMutation, err := application.beginMetadataMutation(ctx, input.StoreID)
	if err != nil {
		_ = application.payloads.Delete(input.StoreID, payload.ID)
		return state.ObjectMetadata{}, err
	}
	defer finishMutation()
	return application.repository.CommitObject(ctx, state.CommitObject{
		ObjectStoreID: input.StoreID, ObjectKey: input.ObjectKey, ContentType: input.ContentType,
		ETag: "\"" + payload.PlaintextSHA256 + "\"", CommittedAtMillis: timestamp,
		Payload: state.ObjectPayload{
			ID: payload.ID, ObjectStoreID: input.StoreID, PlaintextSize: payload.PlaintextSize,
			ChunkCount: payload.ChunkCount, PlaintextSHA256: payload.PlaintextSHA256, CreatedAtMillis: timestamp,
		},
	})
}

func (application *Application) Object(ctx context.Context, storeID, objectKey string) (Object, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return Object{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	metadata, err := application.repository.Object(ctx, storeID, objectKey)
	if err != nil {
		return Object{}, err
	}
	payload, err := application.repository.ObjectPayload(ctx, storeID, metadata.PayloadID)
	if err != nil {
		return Object{}, err
	}
	return Object{Metadata: metadata, Payload: PayloadInfo{
		ID: payload.ID, PlaintextSize: payload.PlaintextSize,
		ChunkCount: payload.ChunkCount, PlaintextSHA256: payload.PlaintextSHA256,
	}}, nil
}

func (application *Application) ReadRange(ctx context.Context, object Object, offset, length int64, output io.Writer) error {
	return application.payloads.ReadRange(ctx, object.Metadata.ObjectStoreID, object.Payload, offset, length, output)
}

func (application *Application) List(ctx context.Context, storeID, prefix, after string, limit int) ([]state.ObjectMetadata, bool, error) {
	return application.repository.ListObjects(ctx, storeID, prefix, after, limit)
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
	return application.repository.DeleteObject(ctx, storeID, objectKey)
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

func (application *Application) signalMetadataLocked(admission *metadataAdmission) {
	close(admission.changed)
	admission.changed = make(chan struct{})
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

func validateObjectKey(value string) error {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return errors.New("object key must be non-empty UTF-8 at most 1024 bytes without NUL")
	}
	return nil
}
