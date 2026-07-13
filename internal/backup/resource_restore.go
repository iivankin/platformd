package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrResourceRestorer           = errors.New("resource restorer is not configured")
	ErrResourceGenerationNotFound = errors.New("resource backup generation not found")
)

type ResourceRestoreOptions struct {
	Mode                 string
	NewResourceName      string
	DestructiveConfirmed bool
}

type ResourceRestoreRequest struct {
	ResourceKind string
	ResourceID   string
	GenerationID string
	Options      ResourceRestoreOptions
	Actor        Actor
	Source       ResourceRestoreSource
}

type ResourceRestoreSource struct {
	Completion     ResourceCompletion
	Envelope       ResourceEnvelope
	Reader         io.Reader
	OpenAttachment func(ResourceAttachment) (io.ReadCloser, error)
}

type ResourceRestorer interface {
	Restore(context.Context, ResourceRestoreRequest) error
}

type ResourceRestorerFunc func(context.Context, ResourceRestoreRequest) error

func (restorer ResourceRestorerFunc) Restore(ctx context.Context, request ResourceRestoreRequest) error {
	return restorer(ctx, request)
}

type ResourceRestoreStore interface {
	BeginOperation(context.Context, state.BeginOperation) error
	SetOperationProgress(context.Context, string, string) error
	FinishOperation(context.Context, state.FinishOperation) error
}

type ResourceRestoreServiceConfig struct {
	Context       context.Context
	Store         ResourceRestoreStore
	Target        *TargetApplication
	TargetGate    *Gate
	Admission     *admission.Gate
	Master        cryptobox.MasterKey
	Restorers     map[string]ResourceRestorer
	RemoteFactory ControlRemoteFactory
	Random        io.Reader
	Now           func() time.Time
	OnError       func(error)
	OnSuccess     func(ResourceRestoreRequest)
}

type ResourceRestoreService struct{ config ResourceRestoreServiceConfig }

func NewResourceRestoreService(config ResourceRestoreServiceConfig) (*ResourceRestoreService, error) {
	if config.Context == nil || config.Store == nil || config.Target == nil || config.TargetGate == nil ||
		config.Admission == nil {
		return nil, errors.New("resource restore service configuration is incomplete")
	}
	for kind, restorer := range config.Restorers {
		if !validBackupResourceKind(kind) || restorer == nil {
			return nil, errors.New("resource restorer map is invalid")
		}
	}
	if config.RemoteFactory == nil {
		config.RemoteFactory = func(config remotes3.Config) (ControlRemote, error) { return remotes3.New(config) }
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
	if config.OnSuccess == nil {
		config.OnSuccess = func(ResourceRestoreRequest) {}
	}
	return &ResourceRestoreService{config: config}, nil
}

func (service *ResourceRestoreService) Start(
	ctx context.Context,
	resourceKind, resourceID, generationID string,
	options ResourceRestoreOptions,
	actor Actor,
) (state.Operation, error) {
	if ctx == nil || !validBackupResourceKind(resourceKind) || !validControlIdentifier(resourceID) ||
		!validControlIdentifier(generationID) {
		return state.Operation{}, errors.New("resource restore request is invalid")
	}
	if err := validateActor(actor); err != nil {
		return state.Operation{}, err
	}
	restorer := service.config.Restorers[resourceKind]
	if restorer == nil {
		return state.Operation{}, ErrResourceRestorer
	}
	if err := service.config.Context.Err(); err != nil {
		return state.Operation{}, err
	}
	releaseTarget, acquired := service.config.TargetGate.TryAcquire()
	if !acquired {
		return state.Operation{}, ErrTargetBusy
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			releaseTarget()
		}
	}()
	target, err := service.config.Target.RuntimeTarget(ctx)
	if errors.Is(err, state.ErrBackupTargetNotFound) {
		return state.Operation{}, ErrResourceTargetMissing
	}
	if err != nil {
		return state.Operation{}, err
	}
	remote, err := service.config.RemoteFactory(remotes3.Config{
		Endpoint: target.Endpoint, Region: target.Region, Bucket: target.Bucket, Prefix: target.Prefix,
		AccessKeyID: target.AccessKeyID, SecretAccessKey: target.SecretAccessKey,
	})
	if err != nil {
		return state.Operation{}, err
	}
	generations, err := ListResourceGenerations(ctx, remote, resourceKind, resourceID)
	if err != nil {
		return state.Operation{}, err
	}
	var completion ResourceCompletion
	for _, candidate := range generations {
		if candidate.GenerationID == generationID {
			completion = candidate
			break
		}
	}
	if completion.GenerationID == "" {
		return state.Operation{}, ErrResourceGenerationNotFound
	}
	startedAt := service.config.Now()
	operationID, err := id.NewWith(startedAt, service.config.Random)
	if err != nil {
		return state.Operation{}, err
	}
	lease, err := service.config.Admission.Begin("resource_restore", operationID)
	if err != nil {
		return state.Operation{}, err
	}
	if err := service.config.Store.BeginOperation(ctx, state.BeginOperation{
		ID: operationID, Kind: resourceKind + "_restore", TargetID: resourceID,
		Progress: "opening_generation", StartedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		lease.Release()
		return state.Operation{}, err
	}
	releaseOnError = false
	operation := state.Operation{
		ID: operationID, Kind: resourceKind + "_restore", TargetID: resourceID,
		Status: "running", Progress: "opening_generation", StartedAtMillis: startedAt.UnixMilli(),
	}
	go service.execute(
		remote, completion, restorer, resourceKind, resourceID, options, actor,
		operationID, lease, releaseTarget,
	)
	return operation, nil
}

func (service *ResourceRestoreService) execute(
	remote ControlRemote,
	completion ResourceCompletion,
	restorer ResourceRestorer,
	resourceKind, resourceID string,
	options ResourceRestoreOptions,
	actor Actor,
	operationID string,
	lease *admission.Lease,
	releaseTarget func(),
) {
	defer releaseTarget()
	defer lease.Release()
	var cause error
	defer func() {
		if recovered := recover(); recovered != nil {
			cause = fmt.Errorf("resource restore panic: %v", recovered)
		}
		service.finish(operationID, resourceKind, cause)
	}()
	reader, err := OpenResource(service.config.Context, remote, service.config.Master, completion)
	if err != nil {
		cause = err
		return
	}
	service.progress(operationID, "restoring")
	counted := &countingResourceReader{source: reader}
	request := ResourceRestoreRequest{
		ResourceKind: resourceKind, ResourceID: resourceID, GenerationID: completion.GenerationID,
		Options: options, Actor: actor,
		Source: ResourceRestoreSource{
			Completion: completion, Envelope: reader.Envelope(), Reader: counted,
			OpenAttachment: func(attachment ResourceAttachment) (io.ReadCloser, error) {
				return OpenResourceAttachment(service.config.Context, remote, reader.Envelope(), attachment)
			},
		},
	}
	if err := restorer.Restore(service.config.Context, request); err != nil {
		cause = err
		return
	}
	if counted.read != reader.Envelope().PlaintextSize {
		cause = errors.New("resource restorer did not consume the complete generation")
		return
	}
	service.config.OnSuccess(request)
}

func (service *ResourceRestoreService) progress(operationID, progress string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := service.config.Store.SetOperationProgress(ctx, operationID, progress)
	cancel()
	if err != nil {
		service.config.OnError(fmt.Errorf("update resource restore operation progress: %w", err))
	}
}

func (service *ResourceRestoreService) finish(operationID, resourceKind string, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := state.FinishOperation{
		ID: operationID, Status: "succeeded", Progress: "complete",
		FinishedAtMillis: service.config.Now().UnixMilli(),
	}
	if cause != nil {
		input.Status = "failed"
		input.Progress = "failed"
		input.ErrorCode = resourceKind + "_restore_failed"
		input.ErrorMessage = boundedBackupError(cause)
		if input.ErrorMessage == "" {
			input.ErrorMessage = "resource restore failed"
		}
	}
	if err := service.config.Store.FinishOperation(ctx, input); err != nil {
		service.config.OnError(fmt.Errorf("finish resource restore operation: %w", err))
	}
	if cause != nil {
		service.config.OnError(cause)
	}
}

type countingResourceReader struct {
	source io.Reader
	read   int64
}

func (reader *countingResourceReader) Read(output []byte) (int, error) {
	count, err := reader.source.Read(output)
	reader.read += int64(count)
	return count, err
}
