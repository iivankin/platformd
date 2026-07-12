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
}

type ApplicationRuntime interface {
	ResolveManagedRedisImage(context.Context, string) (string, error)
	StartManagedRedis(context.Context, string) error
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

type Application struct {
	store   ApplicationStore
	runtime ApplicationRuntime
	master  cryptobox.MasterKey
	random  io.Reader
	now     func() time.Time
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
	return &Application{store: store, runtime: runtime, master: master, random: random, now: now}, nil
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

func (application *Application) Resources(ctx context.Context, projectID string) ([]state.ManagedRedis, error) {
	return application.store.ManagedRedisByProject(ctx, projectID)
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
