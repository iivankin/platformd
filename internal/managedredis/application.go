package managedredis

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrImageUnavailable = errors.New("managed Redis image is unavailable")
	ErrInvalidInput     = errors.New("invalid managed Redis input")
)

type ApplicationStore interface {
	CreateManagedRedis(context.Context, state.CreateManagedRedis) (state.ManagedRedis, error)
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error)
	RecordManagedRedisDataMutation(context.Context, state.RecordManagedRedisDataMutation) error
	RuntimeDeployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error)
	RuntimeDeployment(context.Context, string, string, string) (state.RuntimeDeployment, error)
}

type ApplicationRuntime interface {
	ResolveManagedRedisImage(context.Context, string) (string, error)
	StartManagedRedis(context.Context, string) error
	ManagedRedisPersistence(context.Context, string) (PersistenceStatus, error)
	ManagedRedisStats(context.Context, string) (Stats, error)
	ScanManagedRedisKeys(context.Context, string, ScanQuery) (KeyPage, error)
	PreviewManagedRedisKey(context.Context, string, PreviewQuery) (Preview, error)
	MutateManagedRedis(context.Context, string, Mutation) (MutationResult, error)
	RestartManagedRedisDeployment(context.Context, string, string) error
	RemoveManagedRedisDeployment(context.Context, string, string) error
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type CreateInput struct {
	ProjectID     string
	Name          string
	ImageTag      string
	CPUMillicores int64
	MemoryBytes   int64
	Actor         Actor
}

type CreateResult struct {
	Resource  state.ManagedRedis
	Password  string
	RequestID string
}

type PersistenceReport struct {
	ObservedAtMillis             int64
	LastSuccessfulSaveAtMillis   int64
	ActualRPOMillis              int64
	TargetRPOMillis              int64
	BackgroundSaveInProgress     bool
	LastBackgroundSaveSuccessful bool
	NeedsAttention               bool
}

type Application struct {
	store     ApplicationStore
	runtime   ApplicationRuntime
	master    cryptobox.MasterKey
	random    io.Reader
	now       func() time.Time
	dataSlots chan struct{}
}

func NewApplication(store ApplicationStore, runtime ApplicationRuntime, master cryptobox.MasterKey, random io.Reader, now func() time.Time) (*Application, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("managed Redis application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Application{store: store, runtime: runtime, master: master, random: random, now: now, dataSlots: make(chan struct{}, 4)}, nil
}

func (application *Application) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if input.ProjectID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") {
		return CreateResult{}, fmt.Errorf("%w: create identity is incomplete", ErrInvalidInput)
	}
	if input.Actor.Kind == "access" && input.Actor.Email == "" {
		return CreateResult{}, fmt.Errorf("%w: Access actor email is required", ErrInvalidInput)
	}
	if input.Actor.Kind == "token" && input.Actor.Email != "" {
		return CreateResult{}, fmt.Errorf("%w: token actor cannot carry an Access email", ErrInvalidInput)
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if _, err := managedimages.Reference(managedimages.Redis, input.ImageTag); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.CPUMillicores < 0 || input.MemoryBytes < 0 {
		return CreateResult{}, fmt.Errorf("%w: resource limits cannot be negative", ErrInvalidInput)
	}
	digest, err := application.runtime.ResolveManagedRedisImage(ctx, input.ImageTag)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrImageUnavailable, err)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 4)
	if err != nil {
		return CreateResult{}, err
	}
	password, err := GeneratePasswordWith(application.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate managed Redis password: %w", err)
	}
	encrypted, err := SealPassword(application.master, identifiers[0], password)
	if err != nil {
		return CreateResult{}, err
	}
	created, err := application.store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name,
		ImageTag: input.ImageTag, ImageDigest: digest, VolumeID: identifiers[1],
		PasswordEncrypted: encrypted, CPUMillicores: input.CPUMillicores,
		MemoryMaxBytes: input.MemoryBytes, AuditEventID: identifiers[2],
		ActorKind: input.Actor.Kind, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: identifiers[3], CreatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return CreateResult{}, err
	}
	// Desired state is durable at this point. A transient runtime failure remains
	// visible on the canvas and is retried by an explicit action or daemon restart.
	_ = application.runtime.StartManagedRedis(ctx, created.ID)
	return CreateResult{Resource: created, Password: password, RequestID: identifiers[3]}, nil
}

func (application *Application) Resource(ctx context.Context, projectID, resourceID string) (state.ManagedRedis, error) {
	return application.store.ManagedRedisInProject(ctx, projectID, resourceID)
}

func (application *Application) Password(ctx context.Context, projectID, resourceID string) (string, error) {
	resource, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID)
	if err != nil {
		return "", err
	}
	return OpenPassword(application.master, resource.ID, resource.PasswordEncrypted)
}

func (application *Application) Resources(ctx context.Context, projectID string) ([]state.ManagedRedis, error) {
	return application.store.ManagedRedisByProject(ctx, projectID)
}

func (application *Application) Deployments(ctx context.Context, projectID, resourceID, cursor string, limit int) (state.RuntimeDeploymentPage, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return state.RuntimeDeploymentPage{}, err
	}
	return application.store.RuntimeDeployments(ctx, "redis", resourceID, cursor, limit)
}

func (application *Application) Deployment(ctx context.Context, projectID, resourceID, deploymentID string) (state.RuntimeDeployment, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return state.RuntimeDeployment{}, err
	}
	return application.store.RuntimeDeployment(ctx, "redis", resourceID, deploymentID)
}

func (application *Application) RestartDeployment(ctx context.Context, projectID, resourceID, deploymentID string) error {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return err
	}
	return application.runtime.RestartManagedRedisDeployment(ctx, resourceID, deploymentID)
}

func (application *Application) RemoveDeployment(ctx context.Context, projectID, resourceID, deploymentID string) error {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return err
	}
	return application.runtime.RemoveManagedRedisDeployment(ctx, resourceID, deploymentID)
}

func (application *Application) Persistence(ctx context.Context, projectID, resourceID string) (PersistenceReport, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return PersistenceReport{}, err
	}
	readContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := application.runtime.ManagedRedisPersistence(readContext, resourceID)
	if err != nil {
		return PersistenceReport{}, err
	}
	observedAt := application.now()
	lastSaveMillis := status.LastSuccessfulSaveUnixSeconds * int64(time.Second/time.Millisecond)
	age := observedAt.UnixMilli() - lastSaveMillis
	if age < 0 {
		age = 0
	}
	target := TargetRPO.Milliseconds()
	return PersistenceReport{
		ObservedAtMillis: observedAt.UnixMilli(), LastSuccessfulSaveAtMillis: lastSaveMillis,
		ActualRPOMillis: age, TargetRPOMillis: target,
		BackgroundSaveInProgress:     status.BackgroundSaveInProgress,
		LastBackgroundSaveSuccessful: status.LastBackgroundSaveOK,
		NeedsAttention:               age > target || !status.LastBackgroundSaveOK,
	}, nil
}

func (application *Application) Stats(ctx context.Context, projectID, resourceID string) (Stats, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return Stats{}, err
	}
	readContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return application.runtime.ManagedRedisStats(readContext, resourceID)
}

func (application *Application) Keys(ctx context.Context, projectID, resourceID string, query ScanQuery) (KeyPage, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return KeyPage{}, err
	}
	select {
	case application.dataSlots <- struct{}{}:
		defer func() { <-application.dataSlots }()
	case <-ctx.Done():
		return KeyPage{}, ctx.Err()
	}
	browseContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return application.runtime.ScanManagedRedisKeys(browseContext, resourceID, query)
}

func (application *Application) Preview(ctx context.Context, projectID, resourceID string, query PreviewQuery) (Preview, error) {
	if _, err := application.store.ManagedRedisInProject(ctx, projectID, resourceID); err != nil {
		return Preview{}, err
	}
	select {
	case application.dataSlots <- struct{}{}:
		defer func() { <-application.dataSlots }()
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	}
	browseContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return application.runtime.PreviewManagedRedisKey(browseContext, resourceID, query)
}

type DataMutationInput struct {
	ProjectID  string
	ResourceID string
	Actor      Actor
	Mutation   Mutation
}

type DataMutationResult struct {
	MutationResult
	RequestID     string
	AuditRecorded bool
}

func (application *Application) Mutate(ctx context.Context, input DataMutationInput) (DataMutationResult, error) {
	if input.ProjectID == "" || input.ResourceID == "" || input.Actor.Kind != "access" || input.Actor.ID == "" || input.Actor.Email == "" {
		return DataMutationResult{}, fmt.Errorf("%w: Access identity and Redis target are required", ErrInvalidInput)
	}
	if _, err := application.store.ManagedRedisInProject(ctx, input.ProjectID, input.ResourceID); err != nil {
		return DataMutationResult{}, err
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return DataMutationResult{}, err
	}
	select {
	case application.dataSlots <- struct{}{}:
		defer func() { <-application.dataSlots }()
	case <-ctx.Done():
		return DataMutationResult{}, ctx.Err()
	}
	mutationContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, mutationErr := application.runtime.MutateManagedRedis(mutationContext, input.ResourceID, input.Mutation)
	auditResult := "succeeded"
	if mutationErr != nil {
		auditResult = "failed"
	}
	auditContext, cancelAudit := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelAudit()
	auditErr := application.store.RecordManagedRedisDataMutation(auditContext, state.RecordManagedRedisDataMutation{
		ResourceID: input.ResourceID, ProjectID: input.ProjectID, Operation: string(input.Mutation.Kind),
		Result: auditResult, AuditEventID: identifiers[0], ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[1],
		CreatedAtMillis: timestamp.UnixMilli(),
	})
	if mutationErr != nil {
		return DataMutationResult{}, mutationErr
	}
	return DataMutationResult{
		MutationResult: result, RequestID: identifiers[1], AuditRecorded: auditErr == nil,
	}, nil
}

func (application *Application) identifiers(timestamp time.Time, count int) ([]string, error) {
	result := make([]string, count)
	for index := range result {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return nil, fmt.Errorf("allocate managed Redis identifiers: %w", err)
		}
		result[index] = value
	}
	return result, nil
}
