package managedpostgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrImageUnavailable = errors.New("managed PostgreSQL image is unavailable")
	ErrInvalidInput     = errors.New("invalid managed PostgreSQL input")
)

type Store interface {
	CreateManagedPostgres(context.Context, state.CreateManagedPostgres) (state.ManagedPostgres, error)
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error)
	RecordManagedPostgresExtension(context.Context, state.RecordManagedPostgresExtension) error
	RecordManagedPostgresQuery(context.Context, state.RecordManagedPostgresQuery) error
	BeginOperation(context.Context, state.BeginOperation) error
	SetOperationProgress(context.Context, string, string) error
	FinishOperation(context.Context, state.FinishOperation) error
	RuntimeDeployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error)
	RuntimeDeployment(context.Context, string, string, string) (state.RuntimeDeployment, error)
}

type Runtime interface {
	ResolveManagedPostgresImage(context.Context, string) (string, error)
	StartManagedPostgres(context.Context, string) error
	ManagedPostgresExtensions(context.Context, string) ([]Extension, error)
	ChangeManagedPostgresExtension(context.Context, string, string, bool, func(string)) error
	QueryManagedPostgres(context.Context, string, string) (QueryResult, error)
	RestartManagedPostgresDeployment(context.Context, string, string) error
	RemoveManagedPostgresDeployment(context.Context, string, string) error
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
	Resource      state.ManagedPostgres
	OwnerPassword string
	RequestID     string
}

type Application struct {
	context context.Context
	store   Store
	runtime Runtime
	master  cryptobox.MasterKey
	random  io.Reader
	now     func() time.Time
	slots   chan struct{}
	mu      sync.Mutex
	active  map[string]struct{}
}

func NewApplication(root context.Context, store Store, runtime Runtime, master cryptobox.MasterKey, random io.Reader, now func() time.Time) (*Application, error) {
	if root == nil || store == nil || runtime == nil {
		return nil, errors.New("managed PostgreSQL application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Application{
		context: root, store: store, runtime: runtime, master: master, random: random, now: now,
		slots: make(chan struct{}, 4), active: make(map[string]struct{}),
	}, nil
}

func (application *Application) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if input.ProjectID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") {
		return CreateResult{}, fmt.Errorf("%w: create identity is incomplete", ErrInvalidInput)
	}
	if input.Actor.Kind == "access" && input.Actor.Email == "" {
		return CreateResult{}, fmt.Errorf("%w: Access actor email is required", ErrInvalidInput)
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if _, err := managedimages.Reference(managedimages.PostgreSQL, input.ImageTag); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.CPUMillicores < 0 || input.MemoryBytes < 0 {
		return CreateResult{}, fmt.Errorf("%w: resource limits cannot be negative", ErrInvalidInput)
	}
	digest, err := application.runtime.ResolveManagedPostgresImage(ctx, input.ImageTag)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %v", ErrImageUnavailable, err)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 4)
	if err != nil {
		return CreateResult{}, err
	}
	credentials, err := GenerateCredentials(identifiers[0], application.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate managed PostgreSQL credentials: %w", err)
	}
	ownerEncrypted, err := SealOwnerPassword(application.master, identifiers[0], credentials.OwnerPassword)
	if err != nil {
		return CreateResult{}, err
	}
	bootstrapEncrypted, err := SealBootstrapPassword(application.master, identifiers[0], credentials.BootstrapPassword)
	if err != nil {
		return CreateResult{}, err
	}
	created, err := application.store.CreateManagedPostgres(ctx, state.CreateManagedPostgres{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name,
		ImageTag: input.ImageTag, ImageDigest: digest, VolumeID: identifiers[1],
		DatabaseName: credentials.DatabaseName, OwnerUsername: credentials.OwnerUsername,
		OwnerPasswordEncrypted: ownerEncrypted, BootstrapPasswordEncrypted: bootstrapEncrypted,
		CPUMillicores: input.CPUMillicores, MemoryMaxBytes: input.MemoryBytes,
		AuditEventID: identifiers[2], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[3], CreatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return CreateResult{}, err
	}
	_ = application.runtime.StartManagedPostgres(ctx, created.ID)
	return CreateResult{Resource: created, OwnerPassword: credentials.OwnerPassword, RequestID: identifiers[3]}, nil
}

func (application *Application) Resource(ctx context.Context, projectID, resourceID string) (state.ManagedPostgres, error) {
	return application.store.ManagedPostgresInProject(ctx, projectID, resourceID)
}

func (application *Application) OwnerPassword(ctx context.Context, projectID, resourceID string) (string, error) {
	resource, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID)
	if err != nil {
		return "", err
	}
	return OpenOwnerPassword(application.master, resource.ID, resource.OwnerPasswordEncrypted)
}

func (application *Application) Resources(ctx context.Context, projectID string) ([]state.ManagedPostgres, error) {
	return application.store.ManagedPostgresByProject(ctx, projectID)
}

func (application *Application) Deployments(ctx context.Context, projectID, resourceID, cursor string, limit int) (state.RuntimeDeploymentPage, error) {
	if _, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
		return state.RuntimeDeploymentPage{}, err
	}
	return application.store.RuntimeDeployments(ctx, "postgres", resourceID, cursor, limit)
}

func (application *Application) Deployment(ctx context.Context, projectID, resourceID, deploymentID string) (state.RuntimeDeployment, error) {
	if _, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
		return state.RuntimeDeployment{}, err
	}
	return application.store.RuntimeDeployment(ctx, "postgres", resourceID, deploymentID)
}

func (application *Application) RestartDeployment(ctx context.Context, projectID, resourceID, deploymentID string) error {
	if _, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
		return err
	}
	return application.runtime.RestartManagedPostgresDeployment(ctx, resourceID, deploymentID)
}

func (application *Application) RemoveDeployment(ctx context.Context, projectID, resourceID, deploymentID string) error {
	if _, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
		return err
	}
	return application.runtime.RemoveManagedPostgresDeployment(ctx, resourceID, deploymentID)
}

type QueryInput struct {
	ProjectID  string
	ResourceID string
	Actor      Actor
	SQL        string
}

type QueryOutput struct {
	QueryResult
	RequestID     string
	AuditRecorded bool
}

type ChangeExtensionInput struct {
	ProjectID     string
	ResourceID    string
	Actor         Actor
	ExtensionName string
	Install       bool
}

type ChangeExtensionOutput struct {
	Operation state.Operation
	RequestID string
}

func (application *Application) Extensions(ctx context.Context, projectID, resourceID string) ([]Extension, error) {
	if _, err := application.store.ManagedPostgresInProject(ctx, projectID, resourceID); err != nil {
		return nil, err
	}
	queryContext, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	return application.runtime.ManagedPostgresExtensions(queryContext, resourceID)
}

func (application *Application) ChangeExtension(ctx context.Context, input ChangeExtensionInput) (ChangeExtensionOutput, error) {
	if input.ProjectID == "" || input.ResourceID == "" || input.ExtensionName == "" ||
		input.Actor.Kind != "access" || input.Actor.ID == "" || input.Actor.Email == "" {
		return ChangeExtensionOutput{}, fmt.Errorf("%w: Access identity, PostgreSQL target, and extension are required", ErrInvalidInput)
	}
	if _, err := application.store.ManagedPostgresInProject(ctx, input.ProjectID, input.ResourceID); err != nil {
		return ChangeExtensionOutput{}, err
	}
	if err := application.context.Err(); err != nil {
		return ChangeExtensionOutput{}, err
	}
	if !application.acquireExtension(input.ResourceID) {
		return ChangeExtensionOutput{}, fmt.Errorf("%w: an extension change is already running", ErrInvalidInput)
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			application.releaseExtension(input.ResourceID)
		}
	}()
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 3)
	if err != nil {
		return ChangeExtensionOutput{}, err
	}
	kind := "postgres_extension_install"
	if !input.Install {
		kind = "postgres_extension_uninstall"
	}
	operation := state.Operation{
		ID: identifiers[0], Kind: kind, TargetID: input.ResourceID,
		Status: "running", Progress: "queued", StartedAtMillis: timestamp.UnixMilli(),
	}
	if err := application.store.BeginOperation(ctx, state.BeginOperation{
		ID: operation.ID, Kind: operation.Kind, TargetID: operation.TargetID,
		Progress: operation.Progress, StartedAtMillis: operation.StartedAtMillis,
	}); err != nil {
		return ChangeExtensionOutput{}, err
	}
	releaseOnError = false
	go application.executeExtensionChange(input, operation.ID, identifiers[1], identifiers[2], timestamp)
	return ChangeExtensionOutput{Operation: operation, RequestID: identifiers[2]}, nil
}

func (application *Application) executeExtensionChange(
	input ChangeExtensionInput,
	operationID string,
	auditID string,
	requestID string,
	startedAt time.Time,
) {
	defer application.releaseExtension(input.ResourceID)
	var cause error
	defer func() {
		if recovered := recover(); recovered != nil {
			cause = fmt.Errorf("managed PostgreSQL extension change panic: %v", recovered)
		}
		application.finishExtensionChange(input, operationID, auditID, requestID, startedAt, cause)
	}()
	select {
	case application.slots <- struct{}{}:
		defer func() { <-application.slots }()
	case <-application.context.Done():
		cause = application.context.Err()
		return
	}
	changeContext, cancel := context.WithTimeout(application.context, 30*time.Minute)
	defer cancel()
	cause = application.runtime.ChangeManagedPostgresExtension(
		changeContext, input.ResourceID, input.ExtensionName, input.Install,
		func(value string) { application.extensionProgress(operationID, value) },
	)
}

func (application *Application) extensionProgress(operationID, progress string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// Progress is best-effort; the finished operation records the authoritative result.
	_ = application.store.SetOperationProgress(ctx, operationID, progress)
	cancel()
}

func (application *Application) finishExtensionChange(
	input ChangeExtensionInput,
	operationID string,
	auditID string,
	requestID string,
	startedAt time.Time,
	cause error,
) {
	finishedAt := application.now()
	result := "succeeded"
	finish := state.FinishOperation{
		ID: operationID, Status: "succeeded", Progress: "complete", FinishedAtMillis: finishedAt.UnixMilli(),
	}
	if cause != nil {
		result = "failed"
		finish.Status = "failed"
		finish.Progress = "failed"
		finish.ErrorCode = "postgres_extension_change_failed"
		finish.ErrorMessage = boundedExtensionError(cause)
	}
	auditContext, cancelAudit := context.WithTimeout(context.Background(), 5*time.Second)
	_ = application.store.RecordManagedPostgresExtension(auditContext, state.RecordManagedPostgresExtension{
		ResourceID: input.ResourceID, ProjectID: input.ProjectID,
		ExtensionName: input.ExtensionName, Install: input.Install, Result: result,
		DurationMillis: finishedAt.Sub(startedAt).Milliseconds(), ErrorClass: QueryErrorClass(cause),
		AuditEventID: auditID, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: requestID, CreatedAtMillis: startedAt.UnixMilli(),
	})
	cancelAudit()
	finishContext, cancelFinish := context.WithTimeout(context.Background(), 5*time.Second)
	_ = application.store.FinishOperation(finishContext, finish)
	cancelFinish()
}

func (application *Application) acquireExtension(resourceID string) bool {
	application.mu.Lock()
	defer application.mu.Unlock()
	if _, exists := application.active[resourceID]; exists {
		return false
	}
	application.active[resourceID] = struct{}{}
	return true
}

func (application *Application) releaseExtension(resourceID string) {
	application.mu.Lock()
	delete(application.active, resourceID)
	application.mu.Unlock()
}

func boundedExtensionError(err error) string {
	if err == nil {
		return ""
	}
	value := []rune(err.Error())
	if len(value) > 2048 {
		value = value[:2048]
	}
	return string(value)
}

func (application *Application) Query(ctx context.Context, input QueryInput) (QueryOutput, error) {
	if input.ProjectID == "" || input.ResourceID == "" || input.Actor.Kind != "access" || input.Actor.ID == "" || input.Actor.Email == "" {
		return QueryOutput{}, fmt.Errorf("%w: Access identity and PostgreSQL target are required", ErrInvalidInput)
	}
	if _, err := application.store.ManagedPostgresInProject(ctx, input.ProjectID, input.ResourceID); err != nil {
		return QueryOutput{}, err
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return QueryOutput{}, err
	}
	select {
	case application.slots <- struct{}{}:
		defer func() { <-application.slots }()
	case <-ctx.Done():
		return QueryOutput{}, ctx.Err()
	}
	queryContext, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	started := time.Now()
	result, queryErr := application.runtime.QueryManagedPostgres(queryContext, input.ResourceID, input.SQL)
	duration := time.Since(started)
	rows := 0
	for _, statement := range result.Statements {
		rows += len(statement.Rows)
	}
	auditResult := "succeeded"
	if queryErr != nil {
		auditResult = "failed"
	}
	auditContext, cancelAudit := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelAudit()
	auditErr := application.store.RecordManagedPostgresQuery(auditContext, state.RecordManagedPostgresQuery{
		ResourceID: input.ResourceID, ProjectID: input.ProjectID, Result: auditResult,
		RowCount: rows, DurationMillis: duration.Milliseconds(), ErrorClass: QueryErrorClass(queryErr),
		AuditEventID: identifiers[0], ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: timestamp.UnixMilli(),
	})
	if queryErr != nil {
		return QueryOutput{}, queryErr
	}
	return QueryOutput{QueryResult: result, RequestID: identifiers[1], AuditRecorded: auditErr == nil}, nil
}

func (application *Application) identifiers(timestamp time.Time, count int) ([]string, error) {
	result := make([]string, count)
	for index := range result {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return nil, fmt.Errorf("allocate managed PostgreSQL identifiers: %w", err)
		}
		result[index] = value
	}
	return result, nil
}
