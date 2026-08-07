package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/managedstats"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrManagedStatsKind  = errors.New("invalid managed stats kind")
	ErrManagedStatsRange = errors.New("invalid managed stats range")
)

type ManagedStatsRepository interface {
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error)
}

type ManagedStatsPostgres interface {
	Stats(context.Context, string, string) (managedpostgres.Stats, error)
}

type ManagedStatsRedis interface {
	Stats(context.Context, string, string) (managedredis.Stats, error)
}

type ManagedStatsObjectStore interface {
	Stats(context.Context, string, string) (objectstore.ObjectStoreStats, error)
}

type ManagedStatsHistory interface {
	History(context.Context, string, string, string) (managedstats.History, error)
}

type ManagedStatsConfig struct {
	Repository  ManagedStatsRepository
	Postgres    ManagedStatsPostgres
	Redis       ManagedStatsRedis
	ObjectStore ManagedStatsObjectStore
	History     ManagedStatsHistory
}

type ManagedStatsApplication struct {
	repository  ManagedStatsRepository
	postgres    ManagedStatsPostgres
	redis       ManagedStatsRedis
	objectStore ManagedStatsObjectStore
	history     ManagedStatsHistory
}

type ReadManagedResourceStatsInput struct {
	ProjectID  string
	Kind       string
	ResourceID string
	Range      string
}

func NewManagedStatsApplication(config ManagedStatsConfig) (*ManagedStatsApplication, error) {
	if config.Repository == nil || config.Postgres == nil || config.Redis == nil ||
		config.ObjectStore == nil || config.History == nil {
		return nil, errors.New("managed stats automation dependencies are incomplete")
	}
	return &ManagedStatsApplication{
		repository: config.Repository, postgres: config.Postgres, redis: config.Redis,
		objectStore: config.ObjectStore, history: config.History,
	}, nil
}

func (application *ManagedStatsApplication) Read(
	ctx context.Context,
	identity Identity,
	input ReadManagedResourceStatsInput,
) (any, error) {
	if err := requireReadIdentity(identity); err != nil {
		return nil, err
	}
	if input.ProjectID == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("%w: projectId and resourceId are required", ErrManagedStatsKind)
	}
	kind, err := parseManagedStatsKind(input.Kind)
	if err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, ErrProjectBoundary
	}
	if err := application.verifyResource(ctx, input.ProjectID, kind, input.ResourceID); err != nil {
		return nil, err
	}
	if input.Range == "" {
		return application.liveStats(ctx, input.ProjectID, kind, input.ResourceID)
	}
	if _, ok := usageWindows[input.Range]; !ok {
		return nil, fmt.Errorf("%w: range must be 1h, 6h, 1d, 7d, or 30d", ErrManagedStatsRange)
	}
	history, err := application.history.History(ctx, kind, input.ResourceID, input.Range)
	if err != nil {
		if errors.Is(err, managedstats.ErrInvalidRange) {
			return nil, fmt.Errorf("%w: range must be 1h, 6h, 1d, 7d, or 30d", ErrManagedStatsRange)
		}
		if errors.Is(err, managedstats.ErrInvalidKind) || errors.Is(err, managedstats.ErrInvalidTarget) {
			return nil, fmt.Errorf("%w: %v", ErrManagedStatsKind, err)
		}
		return nil, err
	}
	return history, nil
}

func (application *ManagedStatsApplication) liveStats(
	ctx context.Context,
	projectID, kind, resourceID string,
) (any, error) {
	switch kind {
	case ManagedResourcePostgres:
		return application.postgres.Stats(ctx, projectID, resourceID)
	case ManagedResourceRedis:
		return application.redis.Stats(ctx, projectID, resourceID)
	case ManagedResourceObjectStore:
		return application.objectStore.Stats(ctx, projectID, resourceID)
	default:
		return nil, ErrManagedStatsKind
	}
}

func (application *ManagedStatsApplication) verifyResource(
	ctx context.Context,
	projectID, kind, resourceID string,
) error {
	switch kind {
	case ManagedResourcePostgres:
		_, err := application.repository.ManagedPostgresInProject(ctx, projectID, resourceID)
		return err
	case ManagedResourceRedis:
		_, err := application.repository.ManagedRedisInProject(ctx, projectID, resourceID)
		return err
	case ManagedResourceObjectStore:
		_, err := application.repository.ObjectStoreInProject(ctx, projectID, resourceID)
		return err
	default:
		return ErrManagedStatsKind
	}
}

func parseManagedStatsKind(value string) (string, error) {
	switch value {
	case ManagedResourcePostgres, ManagedResourceRedis, ManagedResourceObjectStore:
		return value, nil
	default:
		return "", fmt.Errorf("%w: kind must be postgres, redis, or object_store", ErrManagedStatsKind)
	}
}
