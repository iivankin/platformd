package databaseversion

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const (
	Postgres = "postgres"
	Redis    = "redis"
)

var (
	ErrUnsupportedKind   = errors.New("managed database kind is unsupported")
	ErrResourceBusy      = errors.New("managed database already has an active lifecycle operation")
	ErrSameDigest        = errors.New("target image digest is already active")
	ErrTargetDigestMoved = errors.New("target image digest changed after preview")
	ErrInsufficientSpace = errors.New("managed database version change needs more free disk space")
	ErrInvalidInput      = errors.New("managed database version change input is invalid")
)

const (
	BlockerSameDigest        = "same_digest"
	BlockerInsufficientSpace = "insufficient_space"
)

type Store interface {
	BeginOperation(context.Context, state.BeginOperation) error
	SetOperationProgress(context.Context, string, string) error
	FinishOperation(context.Context, state.FinishOperation) error
	Operation(context.Context, string) (state.Operation, error)
}

type Resource struct {
	ID          string
	ProjectID   string
	ImageTag    string
	ImageDigest string
	VolumeID    string
}

type Capacity struct {
	CurrentDataBytes  uint64
	RequiredFreeBytes uint64
	AvailableBytes    uint64
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type ChangeRequest struct {
	Resource    Resource
	ImageTag    string
	ImageDigest string
	Actor       Actor
	Progress    func(string)
}

type Adapter interface {
	Resource(context.Context, string, string) (Resource, error)
	Resolve(context.Context, string) (string, error)
	Capacity(context.Context, Resource) (Capacity, error)
	Change(context.Context, ChangeRequest) error
}

type Config struct {
	Context   context.Context
	Store     Store
	Admission *admission.Gate
	Adapters  map[string]Adapter
	Random    io.Reader
	Now       func() time.Time
	OnError   func(error)
}

type Service struct {
	config Config
	mu     sync.Mutex
	active map[string]struct{}
}

type StartResult struct {
	Operation    state.Operation `json:"operation"`
	SourceTag    string          `json:"sourceTag"`
	SourceDigest string          `json:"sourceDigest"`
	TargetTag    string          `json:"targetTag"`
	TargetDigest string          `json:"targetDigest"`
}

type PreviewResult struct {
	SourceTag          string `json:"sourceTag"`
	SourceDigest       string `json:"sourceDigest"`
	TargetTag          string `json:"targetTag"`
	TargetDigest       string `json:"targetDigest"`
	CurrentDataBytes   uint64 `json:"currentDataBytes"`
	RequiredFreeBytes  uint64 `json:"requiredFreeBytes"`
	AvailableFreeBytes uint64 `json:"availableFreeBytes"`
	Ready              bool   `json:"ready"`
	Blocker            string `json:"blocker,omitempty"`
}

func New(config Config) (*Service, error) {
	if config.Context == nil || config.Store == nil || config.Admission == nil || len(config.Adapters) == 0 {
		return nil, errors.New("managed database version service dependencies are incomplete")
	}
	for kind, adapter := range config.Adapters {
		if (kind != Postgres && kind != Redis) || adapter == nil {
			return nil, errors.New("managed database version adapters are invalid")
		}
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OnError == nil {
		config.OnError = func(error) {}
	}
	return &Service{config: config, active: make(map[string]struct{})}, nil
}

func (service *Service) Start(
	ctx context.Context,
	kind string,
	projectID string,
	resourceID string,
	imageTag string,
	expectedTargetDigest string,
	actor Actor,
) (StartResult, error) {
	adapter := service.config.Adapters[kind]
	if adapter == nil {
		return StartResult{}, ErrUnsupportedKind
	}
	expectedTargetDigest = strings.TrimSpace(expectedTargetDigest)
	if ctx == nil || projectID == "" || resourceID == "" || strings.TrimSpace(imageTag) == "" ||
		expectedTargetDigest == "" || !validActor(actor) {
		return StartResult{}, ErrInvalidInput
	}
	if err := service.config.Context.Err(); err != nil {
		return StartResult{}, err
	}
	resourceKey := kind + ":" + resourceID
	if !service.acquire(resourceKey) {
		return StartResult{}, ErrResourceBusy
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			service.release(resourceKey)
		}
	}()
	lease, err := service.config.Admission.Begin("database_version_change", resourceKey)
	if err != nil {
		return StartResult{}, err
	}
	preview, resource, err := service.preview(ctx, adapter, projectID, resourceID, imageTag)
	if err != nil {
		lease.Release()
		return StartResult{}, err
	}
	if preview.TargetDigest != expectedTargetDigest {
		lease.Release()
		return StartResult{}, ErrTargetDigestMoved
	}
	switch preview.Blocker {
	case BlockerSameDigest:
		lease.Release()
		return StartResult{}, ErrSameDigest
	case BlockerInsufficientSpace:
		lease.Release()
		return StartResult{}, ErrInsufficientSpace
	}
	startedAt := service.config.Now()
	operationID, err := id.NewWith(startedAt, service.config.Random)
	if err != nil {
		lease.Release()
		return StartResult{}, err
	}
	operation := state.Operation{
		ID: operationID, Kind: kind + "_version_change", TargetID: resourceID,
		Status: "running", Progress: "resolved_target", StartedAtMillis: startedAt.UnixMilli(),
	}
	if err := service.config.Store.BeginOperation(ctx, state.BeginOperation{
		ID: operation.ID, Kind: operation.Kind, TargetID: operation.TargetID,
		Progress: operation.Progress, StartedAtMillis: operation.StartedAtMillis,
	}); err != nil {
		lease.Release()
		return StartResult{}, err
	}
	releaseOnError = false
	go service.execute(adapter, ChangeRequest{
		Resource: resource, ImageTag: preview.TargetTag, ImageDigest: preview.TargetDigest, Actor: actor,
	}, operationID, resourceKey, lease)
	return StartResult{
		Operation: operation, SourceTag: preview.SourceTag, SourceDigest: preview.SourceDigest,
		TargetTag: preview.TargetTag, TargetDigest: preview.TargetDigest,
	}, nil
}

func (service *Service) Preview(
	ctx context.Context,
	kind string,
	projectID string,
	resourceID string,
	imageTag string,
) (PreviewResult, error) {
	adapter := service.config.Adapters[kind]
	if adapter == nil {
		return PreviewResult{}, ErrUnsupportedKind
	}
	if ctx == nil || projectID == "" || resourceID == "" || strings.TrimSpace(imageTag) == "" {
		return PreviewResult{}, ErrInvalidInput
	}
	if err := service.config.Context.Err(); err != nil {
		return PreviewResult{}, err
	}
	preview, _, err := service.preview(ctx, adapter, projectID, resourceID, imageTag)
	return preview, err
}

func (service *Service) preview(
	ctx context.Context,
	adapter Adapter,
	projectID string,
	resourceID string,
	imageTag string,
) (PreviewResult, Resource, error) {
	imageTag = strings.TrimSpace(imageTag)
	resource, err := adapter.Resource(ctx, projectID, resourceID)
	if err != nil {
		return PreviewResult{}, Resource{}, err
	}
	targetDigest, err := adapter.Resolve(ctx, imageTag)
	if err != nil {
		return PreviewResult{}, Resource{}, err
	}
	capacity, err := adapter.Capacity(ctx, resource)
	if err != nil {
		return PreviewResult{}, Resource{}, err
	}
	preview := PreviewResult{
		SourceTag: resource.ImageTag, SourceDigest: resource.ImageDigest,
		TargetTag: imageTag, TargetDigest: targetDigest,
		CurrentDataBytes:   capacity.CurrentDataBytes,
		RequiredFreeBytes:  capacity.RequiredFreeBytes,
		AvailableFreeBytes: capacity.AvailableBytes,
		Ready:              true,
	}
	if targetDigest == resource.ImageDigest {
		preview.Ready = false
		preview.Blocker = BlockerSameDigest
	} else if capacity.AvailableBytes < capacity.RequiredFreeBytes {
		preview.Ready = false
		preview.Blocker = BlockerInsufficientSpace
	}
	return preview, resource, nil
}

func (service *Service) Operation(
	ctx context.Context,
	kind string,
	projectID string,
	resourceID string,
	operationID string,
) (state.Operation, error) {
	adapter := service.config.Adapters[kind]
	if adapter == nil {
		return state.Operation{}, ErrUnsupportedKind
	}
	if projectID == "" || resourceID == "" || operationID == "" {
		return state.Operation{}, ErrInvalidInput
	}
	if _, err := adapter.Resource(ctx, projectID, resourceID); err != nil {
		return state.Operation{}, err
	}
	operation, err := service.config.Store.Operation(ctx, operationID)
	if err != nil {
		return state.Operation{}, err
	}
	if operation.Kind != kind+"_version_change" || operation.TargetID != resourceID {
		return state.Operation{}, state.ErrOperationNotFound
	}
	return operation, nil
}

func (service *Service) execute(
	adapter Adapter,
	request ChangeRequest,
	operationID string,
	resourceKey string,
	lease *admission.Lease,
) {
	defer lease.Release()
	defer service.release(resourceKey)
	var cause error
	defer func() {
		if recovered := recover(); recovered != nil {
			cause = fmt.Errorf("managed database version change panic: %v", recovered)
		}
		service.finish(operationID, request.Resource.ID, cause)
	}()
	request.Progress = func(value string) { service.progress(operationID, value) }
	cause = adapter.Change(service.config.Context, request)
}

func (service *Service) progress(operationID, progress string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := service.config.Store.SetOperationProgress(ctx, operationID, progress)
	cancel()
	if err != nil {
		service.config.OnError(fmt.Errorf("update managed database version progress: %w", err))
	}
}

func (service *Service) finish(operationID, resourceID string, cause error) {
	input := state.FinishOperation{
		ID: operationID, Status: "succeeded", Progress: "complete",
		FinishedAtMillis: service.config.Now().UnixMilli(),
	}
	if cause != nil {
		input.Status = "failed"
		input.Progress = "failed"
		input.ErrorCode = "database_version_change_failed"
		input.ErrorMessage = boundedError(cause)
		if input.ErrorMessage == "" {
			input.ErrorMessage = "managed database version change failed"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := service.config.Store.FinishOperation(ctx, input)
	cancel()
	if err != nil {
		service.config.OnError(fmt.Errorf("finish managed database version operation: %w", err))
	}
	if cause != nil {
		service.config.OnError(fmt.Errorf("managed database %s version change: %w", resourceID, cause))
	}
}

func (service *Service) acquire(key string) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	if _, exists := service.active[key]; exists {
		return false
	}
	service.active[key] = struct{}{}
	return true
}

func (service *Service) release(key string) {
	service.mu.Lock()
	delete(service.active, key)
	service.mu.Unlock()
}

func validActor(actor Actor) bool {
	if actor.ID == "" {
		return false
	}
	switch actor.Kind {
	case "access":
		return actor.Email != ""
	case "token":
		return actor.Email == ""
	default:
		return false
	}
}

func boundedError(err error) string {
	const limit = 4096
	value := strings.ToValidUTF8(err.Error(), "�")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
