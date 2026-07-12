package managedpostgres

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
	ErrImageUnavailable = errors.New("managed PostgreSQL image is unavailable")
	ErrInvalidInput     = errors.New("invalid managed PostgreSQL input")
)

type Store interface {
	CreateManagedPostgres(context.Context, state.CreateManagedPostgres) (state.ManagedPostgres, error)
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error)
	RecordManagedPostgresQuery(context.Context, state.RecordManagedPostgresQuery) error
}

type Runtime interface {
	ResolveManagedPostgresImage(context.Context, string) (string, error)
	StartManagedPostgres(context.Context, string) error
	QueryManagedPostgres(context.Context, string, string) (QueryResult, error)
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
	store   Store
	runtime Runtime
	master  cryptobox.MasterKey
	random  io.Reader
	now     func() time.Time
	slots   chan struct{}
}

func NewApplication(store Store, runtime Runtime, master cryptobox.MasterKey, random io.Reader, now func() time.Time) (*Application, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("managed PostgreSQL application dependencies are incomplete")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Application{store: store, runtime: runtime, master: master, random: random, now: now, slots: make(chan struct{}, 4)}, nil
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

func (application *Application) Resources(ctx context.Context, projectID string) ([]state.ManagedPostgres, error) {
	return application.store.ManagedPostgresByProject(ctx, projectID)
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
