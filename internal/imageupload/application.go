package imageupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

const (
	UploadRetention  = 24 * time.Hour
	PreviewRetention = 14 * 24 * time.Hour
)

var (
	imageTag         = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	sha256Hex        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uploadIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{15,127}$`)
)

type Store interface {
	Service(context.Context, string, string) (state.ServiceDesired, error)
	BeginImageUpload(context.Context, state.BeginImageUploadInput) ([]string, error)
	ImageUpload(context.Context, string, string) (state.ImageUpload, error)
	AdvanceImageUpload(context.Context, string, int64, int64, int64) error
	CreateImageRevision(context.Context, state.CreateImageRevisionInput) error
	SetImageRevisionImported(context.Context, string, string, string, string, int64) error
	CompleteImageUpload(context.Context, string, string, string, int64, int64, int64) error
	FailImageUpload(context.Context, string, string, string, int64, int64) error
	ActiveImageUploads(context.Context, int64, bool) ([]state.ImageUpload, error)
	CancelImageUploads(context.Context, int64, bool, int64) ([]string, error)
}

type Engine interface {
	Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error)
}

type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type Runtime interface {
	DeployUploadedProduction(context.Context, string, string, string, string, containerengine.Image, state.ImageUploadIdentity) error
	DeployUploadedPreview(context.Context, string, string, string, string, string, containerengine.Image, state.ImageUploadIdentity) (string, error)
}

type Config struct {
	Context        context.Context
	Store          Store
	Engine         Engine
	Runtime        Runtime
	Growth         GrowthGate
	Verifier       *OIDCVerifier
	PublicHostname string
	UploadRoot     string
	ImageRoot      string
	Now            func() time.Time
	NewID          func() (string, error)
	OnError        func(error)
}

type Application struct {
	context    context.Context
	store      Store
	engine     Engine
	runtime    Runtime
	growth     GrowthGate
	verifier   *OIDCVerifier
	hostname   string
	uploadRoot string
	imageRoot  string
	now        func() time.Time
	newID      func() (string, error)
	onError    func(error)

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	runs  map[string]runningUpload
}

type runningUpload struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

func New(config Config) (*Application, error) {
	if config.Context == nil || config.Store == nil || config.Engine == nil || config.Runtime == nil || config.Growth == nil ||
		config.Verifier == nil || config.PublicHostname == "" || !safeRoot(config.UploadRoot) || !safeRoot(config.ImageRoot) {
		return nil, errors.New("image upload dependencies are incomplete")
	}
	if err := os.MkdirAll(config.UploadRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create image upload root: %w", err)
	}
	if err := os.MkdirAll(config.ImageRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create image revision root: %w", err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = id.New
	}
	onError := config.OnError
	if onError == nil {
		onError = func(error) {}
	}
	return &Application{
		context: config.Context, store: config.Store, engine: config.Engine, runtime: config.Runtime,
		verifier: config.Verifier, hostname: config.PublicHostname, uploadRoot: config.UploadRoot,
		imageRoot: config.ImageRoot, now: now, newID: newID, onError: onError,
		growth: config.Growth, locks: make(map[string]*sync.Mutex), runs: make(map[string]runningUpload),
	}, nil
}

func (application *Application) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /public/api/v1/projects/{projectID}/services/{serviceID}/image", application.status)
	mux.HandleFunc("POST /public/api/v1/projects/{projectID}/services/{serviceID}/image", application.upload)
	return mux
}

func (application *Application) status(response http.ResponseWriter, request *http.Request) {
	service, upload, ok := application.authorizeExisting(response, request)
	if !ok {
		return
	}
	_ = service
	writeUploadResponse(response, http.StatusOK, upload)
}

func (application *Application) upload(response http.ResponseWriter, request *http.Request) {
	service, tag, identity, ok := application.authorizeUpload(response, request)
	if !ok {
		return
	}
	uploadID := strings.TrimSpace(request.Header.Get("Upload-ID"))
	if !uploadIdentifier.MatchString(uploadID) {
		writeUploadError(response, http.StatusBadRequest, "upload_id_required", "Upload-ID is required")
		return
	}
	lock := application.lock(service.ID + ":" + uploadID)
	lock.Lock()
	defer lock.Unlock()

	upload, err := application.store.ImageUpload(request.Context(), uploadID, service.ID)
	if errors.Is(err, state.ErrImageUploadNotFound) {
		upload, err = application.begin(request.Context(), request, service, tag, identity)
	}
	if err != nil {
		application.writeRequestError(response, err)
		return
	}
	if upload.Tag != tag || !sameRun(identity, upload.Identity) {
		writeOIDCError(response)
		return
	}
	if upload.Status != "uploading" {
		writeUploadResponse(response, http.StatusAccepted, upload)
		return
	}
	offset, err := parseNonNegativeHeader(request.Header.Get("Upload-Offset"))
	if err != nil || offset != upload.ReceivedLength {
		application.fail(upload.ID, "offset_mismatch", errors.New("Upload-Offset does not match the stored offset"))
		_ = os.Remove(upload.TemporaryPath)
		writeUploadError(response, http.StatusConflict, "offset_mismatch", "Upload-Offset does not match the stored offset")
		return
	}
	if request.ContentLength == 0 {
		writeUploadError(response, http.StatusBadRequest, "empty_chunk", "Upload chunk is empty")
		return
	}
	remaining := upload.ExpectedLength - upload.ReceivedLength
	if remaining <= 0 || (request.ContentLength > 0 && request.ContentLength > remaining) {
		writeUploadError(response, http.StatusRequestEntityTooLarge, "chunk_exceeds_upload", "Chunk exceeds Upload-Length")
		return
	}
	next, err := appendChunk(upload.TemporaryPath, upload.ReceivedLength, remaining, request.Body)
	if err != nil {
		application.fail(upload.ID, "chunk_write_failed", err)
		writeUploadError(response, http.StatusInternalServerError, "chunk_write_failed", "Unable to store upload chunk")
		return
	}
	if err := application.store.AdvanceImageUpload(request.Context(), upload.ID, upload.ReceivedLength, next, application.now().UnixMilli()); err != nil {
		_ = os.Truncate(upload.TemporaryPath, upload.ReceivedLength)
		application.writeRequestError(response, err)
		return
	}
	upload.ReceivedLength = next
	upload.UpdatedAtMillis = application.now().UnixMilli()
	if next == upload.ExpectedLength {
		go application.process(service.ID, upload.ID)
	}
	writeUploadResponse(response, http.StatusAccepted, upload)
}

func (application *Application) begin(
	ctx context.Context,
	request *http.Request,
	service state.ServiceDesired,
	tag string,
	identity state.ImageUploadIdentity,
) (state.ImageUpload, error) {
	if err := application.growth.PermitGrowth(ctx); err != nil {
		return state.ImageUpload{}, err
	}
	offset, err := parseNonNegativeHeader(request.Header.Get("Upload-Offset"))
	if err != nil || offset != 0 {
		return state.ImageUpload{}, requestError{"invalid_offset", "First Upload-Offset must be 0"}
	}
	length, err := parsePositiveHeader(request.Header.Get("Upload-Length"))
	if err != nil {
		return state.ImageUpload{}, requestError{"invalid_length", "Upload-Length must be positive"}
	}
	expectedSHA := strings.TrimSpace(request.Header.Get("Upload-SHA256"))
	if !sha256Hex.MatchString(expectedSHA) {
		return state.ImageUpload{}, requestError{"invalid_sha256", "Upload-SHA256 must be lowercase SHA-256 hex"}
	}
	uploadID := strings.TrimSpace(request.Header.Get("Upload-ID"))
	temporaryPath := filepath.Join(application.uploadRoot, uploadID+".part")
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return state.ImageUpload{}, fmt.Errorf("create upload file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return state.ImageUpload{}, err
	}
	now := application.now()
	superseded, err := application.store.BeginImageUpload(ctx, state.BeginImageUploadInput{
		ID: uploadID, ServiceID: service.ID, Tag: tag, ExpectedLength: length,
		ExpectedSHA256: expectedSHA, TemporaryPath: temporaryPath, Identity: identity,
		CreatedAtMillis: now.UnixMilli(), ExpiresAtMillis: now.Add(UploadRetention).UnixMilli(),
	})
	if err != nil {
		_ = os.Remove(temporaryPath)
		return state.ImageUpload{}, err
	}
	for _, path := range superseded {
		if path != temporaryPath {
			_ = os.Remove(path)
		}
	}
	return application.store.ImageUpload(ctx, uploadID, service.ID)
}

// authorizeUpload validates OIDC before any distinguishing service/upload errors.
func (application *Application) authorizeUpload(
	response http.ResponseWriter,
	request *http.Request,
) (state.ServiceDesired, string, state.ImageUploadIdentity, bool) {
	if !hasBearer(request) {
		writeOIDCError(response)
		return state.ServiceDesired{}, "", state.ImageUploadIdentity{}, false
	}
	service, err := application.store.Service(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
	if err != nil || !service.Enabled ||
		service.Snapshot.Source.Type != servicesource.DockerImageUpload || service.Snapshot.Source.DockerUpload == nil {
		writeOIDCError(response)
		return state.ServiceDesired{}, "", state.ImageUploadIdentity{}, false
	}
	tag := strings.TrimSpace(request.Header.Get("Upload-Tag"))
	if !imageTag.MatchString(tag) {
		writeOIDCError(response)
		return state.ServiceDesired{}, "", state.ImageUploadIdentity{}, false
	}
	identity, err := application.verify(request.Context(), request, service, tag)
	if err != nil {
		writeOIDCError(response)
		return state.ServiceDesired{}, "", state.ImageUploadIdentity{}, false
	}
	return service, tag, identity, true
}

func (application *Application) authorizeExisting(response http.ResponseWriter, request *http.Request) (state.ServiceDesired, state.ImageUpload, bool) {
	if !hasBearer(request) {
		writeOIDCError(response)
		return state.ServiceDesired{}, state.ImageUpload{}, false
	}
	service, err := application.store.Service(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
	if err != nil || !service.Enabled ||
		service.Snapshot.Source.Type != servicesource.DockerImageUpload || service.Snapshot.Source.DockerUpload == nil {
		writeOIDCError(response)
		return state.ServiceDesired{}, state.ImageUpload{}, false
	}
	uploadID := strings.TrimSpace(request.Header.Get("Upload-ID"))
	upload, err := application.store.ImageUpload(request.Context(), uploadID, service.ID)
	if err != nil {
		writeOIDCError(response)
		return state.ServiceDesired{}, state.ImageUpload{}, false
	}
	identity, err := application.verify(request.Context(), request, service, upload.Tag)
	if err != nil || !sameRun(identity, upload.Identity) {
		writeOIDCError(response)
		return state.ServiceDesired{}, state.ImageUpload{}, false
	}
	return service, upload, true
}

func (application *Application) verify(ctx context.Context, request *http.Request, service state.ServiceDesired, tag string) (state.ImageUploadIdentity, error) {
	source := service.Snapshot.Source.DockerUpload
	if source == nil || !hasBearer(request) {
		return state.ImageUploadIdentity{}, ErrOIDC
	}
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	audience := "https://" + application.hostname + request.URL.Path
	return application.verifier.Verify(ctx, OIDCRequest{
		Token: strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")), Audience: audience,
		Repository: source.Repository, Branch: source.Branch, Workflows: source.Workflows,
		Production: tag == "latest",
	})
}

func hasBearer(request *http.Request) bool {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	return strings.HasPrefix(authorization, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")) != ""
}

func writeOIDCError(response http.ResponseWriter) {
	writeUploadError(response, http.StatusUnauthorized, "invalid_oidc", "GitHub Actions OIDC token is invalid")
}

func (application *Application) process(serviceID, uploadID string) {
	processContext, cancel := context.WithCancel(application.context)
	done := make(chan struct{})
	application.registerRun(uploadID, runningUpload{cancel: cancel, done: done})
	defer func() {
		cancel()
		close(done)
		application.unregisterRun(uploadID)
	}()
	upload, err := application.store.ImageUpload(processContext, uploadID, serviceID)
	if err != nil {
		application.onError(err)
		return
	}
	lock := application.lock(serviceID + ":" + upload.Tag)
	lock.Lock()
	defer lock.Unlock()
	upload, err = application.store.ImageUpload(processContext, uploadID, serviceID)
	if err != nil || upload.Status != "uploading" || upload.ReceivedLength != upload.ExpectedLength {
		return
	}
	actualSHA, err := fileSHA256(upload.TemporaryPath)
	if err != nil || actualSHA != upload.ExpectedSHA256 {
		if err == nil {
			err = errors.New("uploaded archive SHA-256 does not match Upload-SHA256")
		}
		application.fail(upload.ID, "sha256_mismatch", err)
		_ = os.Remove(upload.TemporaryPath)
		return
	}
	revisionID, err := application.newID()
	if err != nil {
		application.fail(upload.ID, "id_allocation_failed", err)
		return
	}
	revisionDirectory := filepath.Join(application.imageRoot, serviceID)
	if err := os.MkdirAll(revisionDirectory, 0o700); err != nil {
		application.fail(upload.ID, "archive_store_failed", err)
		return
	}
	archivePath := filepath.Join(revisionDirectory, revisionID+".oci")
	if err := os.Rename(upload.TemporaryPath, archivePath); err != nil {
		application.fail(upload.ID, "archive_store_failed", err)
		return
	}
	kind := "preview"
	expires := application.now().Add(PreviewRetention).UnixMilli()
	if upload.Tag == "latest" {
		kind = "production"
		expires = 0
	}
	if err := application.store.CreateImageRevision(processContext, state.CreateImageRevisionInput{
		ID: revisionID, UploadID: upload.ID, ServiceID: serviceID, Tag: upload.Tag, Kind: kind,
		ArchivePath: archivePath, ArchiveSHA256: actualSHA, Identity: upload.Identity,
		CreatedAtMillis: application.now().UnixMilli(), ExpiresAtMillis: expires,
	}); err != nil {
		application.fail(upload.ID, "state_update_failed", err)
		_ = os.Remove(archivePath)
		return
	}
	image, err := application.engine.Pull(processContext, containerengine.PullRequest{Reference: "oci-archive:" + archivePath})
	if err != nil || image.OS != "linux" || image.Architecture != "amd64" {
		if err == nil {
			err = fmt.Errorf("image platform is %s/%s, want linux/amd64", image.OS, image.Architecture)
		}
		application.fail(upload.ID, "invalid_oci_archive", err)
		_ = os.Remove(archivePath)
		return
	}
	deploymentID, err := application.newID()
	if err != nil {
		application.fail(upload.ID, "id_allocation_failed", err)
		return
	}
	previewID := ""
	if kind == "preview" {
		previewID = deploymentID
	}
	if err := application.store.SetImageRevisionImported(processContext, revisionID, upload.ID, image.Digest, previewID, application.now().UnixMilli()); err != nil {
		application.fail(upload.ID, "state_update_failed", err)
		return
	}
	previewURL := ""
	if kind == "production" {
		err = application.runtime.DeployUploadedProduction(processContext, serviceID, deploymentID, revisionID, "oci-archive:"+archivePath, image, upload.Identity)
	} else {
		previewURL, err = application.runtime.DeployUploadedPreview(processContext, serviceID, previewID, upload.Tag, revisionID, "oci-archive:"+archivePath, image, upload.Identity)
	}
	if err != nil {
		application.fail(upload.ID, "deployment_failed", err)
		return
	}
	now := application.now()
	if err := application.store.CompleteImageUpload(
		processContext, upload.ID, revisionID, previewURL, now.UnixMilli(),
		now.Add(UploadRetention).UnixMilli(), now.Add(PreviewRetention).UnixMilli(),
	); err != nil {
		// Deploy already published the revision; clear the in-flight upload slot so
		// the tag unique index does not block the next push.
		application.fail(upload.ID, "completion_failed", err)
		application.onError(err)
	}
}

func (application *Application) fail(uploadID, code string, cause error) {
	if cause == nil {
		cause = errors.New(code)
	}
	now := application.now()
	if err := application.store.FailImageUpload(context.WithoutCancel(application.context), uploadID, code, cause.Error(), now.UnixMilli(), now.Add(UploadRetention).UnixMilli()); err != nil && !errors.Is(err, state.ErrImageUploadChanged) {
		application.onError(errors.Join(cause, err))
	}
}

func (application *Application) lock(key string) *sync.Mutex {
	application.mu.Lock()
	defer application.mu.Unlock()
	lock := application.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		application.locks[key] = lock
	}
	return lock
}

func (application *Application) CancelExpired(ctx context.Context, now time.Time) error {
	return application.cancelUploads(ctx, now.UnixMilli(), false, now)
}

func (application *Application) CancelAll(ctx context.Context, now time.Time) error {
	return application.cancelUploads(ctx, now.UnixMilli(), true, now)
}

func (application *Application) cancelUploads(ctx context.Context, expiresAtOrBefore int64, all bool, now time.Time) error {
	uploads, err := application.store.ActiveImageUploads(ctx, expiresAtOrBefore, all)
	if err != nil {
		return err
	}
	application.mu.Lock()
	runs := make([]runningUpload, 0, len(uploads))
	for _, upload := range uploads {
		if run, ok := application.runs[upload.ID]; ok {
			run.cancel()
			runs = append(runs, run)
		}
	}
	application.mu.Unlock()
	for _, run := range runs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-run.done:
		}
	}
	paths, err := application.store.CancelImageUploads(context.WithoutCancel(ctx), expiresAtOrBefore, all, now.UnixMilli())
	for _, path := range paths {
		_ = os.Remove(path)
	}
	return err
}

func (application *Application) registerRun(uploadID string, run runningUpload) {
	application.mu.Lock()
	application.runs[uploadID] = run
	application.mu.Unlock()
}

func (application *Application) unregisterRun(uploadID string) {
	application.mu.Lock()
	delete(application.runs, uploadID)
	application.mu.Unlock()
}

func appendChunk(path string, offset, remaining int64, body io.Reader) (int64, error) {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return offset, err
	}
	defer file.Close()
	if position, err := file.Seek(offset, io.SeekStart); err != nil || position != offset {
		return offset, errors.Join(err, errors.New("seek upload file failed"))
	}
	written, err := io.Copy(file, io.LimitReader(body, remaining+1))
	if err != nil {
		return offset, err
	}
	if written == 0 {
		return offset, errors.New("upload chunk is empty")
	}
	if written > remaining {
		return offset, errors.New("upload chunk exceeds declared length")
	}
	if err := file.Sync(); err != nil {
		return offset, err
	}
	return offset + written, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parsePositiveHeader(value string) (int64, error) {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("positive integer required")
	}
	return number, nil
}
func parseNonNegativeHeader(value string) (int64, error) {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number < 0 {
		return 0, errors.New("non-negative integer required")
	}
	return number, nil
}
func safeRoot(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/"
}

func sameRun(left, right state.ImageUploadIdentity) bool {
	return left.Repository == right.Repository && left.Ref == right.Ref && left.WorkflowRef == right.WorkflowRef && left.RunID == right.RunID && left.RunAttempt == right.RunAttempt
}

type requestError struct{ code, message string }

func (err requestError) Error() string { return err.message }

func (application *Application) writeRequestError(response http.ResponseWriter, err error) {
	var invalid requestError
	switch {
	case errors.As(err, &invalid):
		writeUploadError(response, http.StatusBadRequest, invalid.code, invalid.message)
	case errors.Is(err, ErrOIDC):
		writeUploadError(response, http.StatusUnauthorized, "invalid_oidc", "GitHub Actions OIDC token is invalid")
	case errors.Is(err, state.ErrImageUploadNotFound):
		writeUploadError(response, http.StatusNotFound, "upload_not_found", "Image upload not found")
	case errors.Is(err, state.ErrImageUploadChanged):
		writeUploadError(response, http.StatusConflict, "upload_changed", "Image upload changed")
	default:
		writeUploadError(response, http.StatusInternalServerError, "internal_error", "Unable to process image upload")
	}
}
