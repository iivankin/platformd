package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/managedstats"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

type managedStatsRepositoryStub struct {
	postgresCalls int
	redisCalls    int
	objectCalls   int
}

func (repository *managedStatsRepositoryStub) ManagedPostgresInProject(
	_ context.Context, projectID, resourceID string,
) (state.ManagedPostgres, error) {
	repository.postgresCalls++
	if projectID != "project" || resourceID != "postgres" {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return state.ManagedPostgres{ID: "postgres", ProjectID: "project"}, nil
}

func (repository *managedStatsRepositoryStub) ManagedRedisInProject(
	_ context.Context, projectID, resourceID string,
) (state.ManagedRedis, error) {
	repository.redisCalls++
	if projectID != "project" || resourceID != "redis" {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return state.ManagedRedis{ID: "redis", ProjectID: "project"}, nil
}

func (repository *managedStatsRepositoryStub) ObjectStoreInProject(
	_ context.Context, projectID, resourceID string,
) (state.ObjectStore, error) {
	repository.objectCalls++
	if projectID != "project" || resourceID != "store" {
		return state.ObjectStore{}, state.ErrObjectStoreNotFound
	}
	return state.ObjectStore{ID: "store", ProjectID: "project"}, nil
}

type managedStatsPostgresStub struct{ calls int }

func (stub *managedStatsPostgresStub) Stats(context.Context, string, string) (managedpostgres.Stats, error) {
	stub.calls++
	return managedpostgres.Stats{Connections: 3, QueriesPerSecond: 12}, nil
}

type managedStatsRedisStub struct{ calls int }

func (stub *managedStatsRedisStub) Stats(context.Context, string, string) (managedredis.Stats, error) {
	stub.calls++
	return managedredis.Stats{ConnectedClients: 2, OperationsPerSecond: 40}, nil
}

type managedStatsObjectStoreStub struct{ calls int }

func (stub *managedStatsObjectStoreStub) Stats(context.Context, string, string) (objectstore.ObjectStoreStats, error) {
	stub.calls++
	return objectstore.ObjectStoreStats{Ready: true, ObjectCount: 9, TotalBytes: 100}, nil
}

type managedStatsHistoryStub struct {
	calls      int
	kind       string
	resourceID string
	rangeName  string
}

func (stub *managedStatsHistoryStub) History(
	_ context.Context, kind, resourceID, rangeName string,
) (managedstats.History, error) {
	stub.calls++
	stub.kind, stub.resourceID, stub.rangeName = kind, resourceID, rangeName
	if rangeName == "bad" {
		return managedstats.History{}, managedstats.ErrInvalidRange
	}
	return managedstats.History{
		From: 1, To: 2, StepMillis: 60_000,
		Points: []managedstats.Point{{ObservedAt: 1, Metrics: map[string]any{"queriesPerSecond": 5.0}}},
	}, nil
}

func TestManagedStatsReadLiveAndHistory(t *testing.T) {
	t.Parallel()
	repository := &managedStatsRepositoryStub{}
	postgres := &managedStatsPostgresStub{}
	redis := &managedStatsRedisStub{}
	objects := &managedStatsObjectStoreStub{}
	history := &managedStatsHistoryStub{}
	application, err := NewManagedStatsApplication(ManagedStatsConfig{
		Repository: repository, Postgres: postgres, Redis: redis, ObjectStore: objects, History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := Identity{TokenID: "read", Role: "read"}

	live, err := application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "project", Kind: ManagedResourceRedis, ResourceID: "redis",
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, ok := live.(managedredis.Stats)
	if !ok || stats.OperationsPerSecond != 40 || redis.calls != 1 || history.calls != 0 {
		t.Fatalf("live redis = %+v ok=%v redisCalls=%d historyCalls=%d", live, ok, redis.calls, history.calls)
	}

	rolled, err := application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "project", Kind: ManagedResourcePostgres, ResourceID: "postgres", Range: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	points, ok := rolled.(managedstats.History)
	if !ok || len(points.Points) != 1 || history.kind != "postgres" || history.rangeName != "1h" || postgres.calls != 0 {
		t.Fatalf("history = %+v ok=%v history=%+v postgresCalls=%d", rolled, ok, history, postgres.calls)
	}

	objectLive, err := application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "project", Kind: ManagedResourceObjectStore, ResourceID: "store",
	})
	if err != nil {
		t.Fatal(err)
	}
	objectStats, ok := objectLive.(objectstore.ObjectStoreStats)
	if !ok || !objectStats.Ready || objects.calls != 1 {
		t.Fatalf("object live = %+v", objectLive)
	}
}

func TestManagedStatsEnforcesBoundaryAndValidation(t *testing.T) {
	t.Parallel()
	repository := &managedStatsRepositoryStub{}
	history := &managedStatsHistoryStub{}
	application, err := NewManagedStatsApplication(ManagedStatsConfig{
		Repository: repository, Postgres: &managedStatsPostgresStub{}, Redis: &managedStatsRedisStub{},
		ObjectStore: &managedStatsObjectStoreStub{}, History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	bound := "project"
	identity := Identity{TokenID: "read", Role: "read", ProjectID: &bound}

	_, err = application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "other", Kind: ManagedResourceRedis, ResourceID: "redis",
	})
	if !errors.Is(err, ErrProjectBoundary) || repository.redisCalls != 0 {
		t.Fatalf("boundary = %v calls=%d", err, repository.redisCalls)
	}

	_, err = application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "project", Kind: "service", ResourceID: "redis",
	})
	if !errors.Is(err, ErrManagedStatsKind) {
		t.Fatalf("kind = %v", err)
	}

	_, err = application.Read(context.Background(), identity, ReadManagedResourceStatsInput{
		ProjectID: "project", Kind: ManagedResourcePostgres, ResourceID: "postgres", Range: "2h",
	})
	if !errors.Is(err, ErrManagedStatsRange) || history.calls != 0 {
		t.Fatalf("range = %v historyCalls=%d", err, history.calls)
	}
}
