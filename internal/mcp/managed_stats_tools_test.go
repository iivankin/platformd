package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/managedstats"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

type mcpManagedStatsRepository struct {
	redisCalls int
}

func (*mcpManagedStatsRepository) ManagedPostgresInProject(
	context.Context, string, string,
) (state.ManagedPostgres, error) {
	return state.ManagedPostgres{ID: "postgres", ProjectID: "project"}, nil
}

func (repository *mcpManagedStatsRepository) ManagedRedisInProject(
	_ context.Context, projectID, _ string,
) (state.ManagedRedis, error) {
	repository.redisCalls++
	if projectID != "project" {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return state.ManagedRedis{ID: "redis", ProjectID: "project"}, nil
}

func (*mcpManagedStatsRepository) ObjectStoreInProject(
	context.Context, string, string,
) (state.ObjectStore, error) {
	return state.ObjectStore{ID: "store", ProjectID: "project"}, nil
}

type mcpManagedStatsPostgres struct{}

func (mcpManagedStatsPostgres) Stats(context.Context, string, string) (managedpostgres.Stats, error) {
	return managedpostgres.Stats{Connections: 4}, nil
}

type mcpManagedStatsRedis struct{ calls int }

func (stats *mcpManagedStatsRedis) Stats(context.Context, string, string) (managedredis.Stats, error) {
	stats.calls++
	return managedredis.Stats{OperationsPerSecond: 25}, nil
}

type mcpManagedStatsObjectStore struct{}

func (mcpManagedStatsObjectStore) Stats(context.Context, string, string) (objectstore.ObjectStoreStats, error) {
	return objectstore.ObjectStoreStats{Ready: true, ObjectCount: 3}, nil
}

type mcpManagedStatsHistory struct {
	calls     int
	kind      string
	rangeName string
}

func (history *mcpManagedStatsHistory) History(
	_ context.Context, kind, _, rangeName string,
) (managedstats.History, error) {
	history.calls++
	history.kind = kind
	history.rangeName = rangeName
	return managedstats.History{
		From: 10, To: 20, StepMillis: 60_000,
		Points: []managedstats.Point{{
			ObservedAt: 10,
			Metrics:    map[string]any{"operationsPerSecond": 7.0},
		}},
	}, nil
}

func TestMCPReadManagedResourceStats(t *testing.T) {
	repository := &mcpManagedStatsRepository{}
	redis := &mcpManagedStatsRedis{}
	history := &mcpManagedStatsHistory{}
	application, err := automation.NewManagedStatsApplication(automation.ManagedStatsConfig{
		Repository: repository, Postgres: mcpManagedStatsPostgres{}, Redis: redis,
		ObjectStore: mcpManagedStatsObjectStore{}, History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, &repositoryStub{})
	handler.managedStats = application
	handler.tools = append(handler.tools, readManagedResourceStatsTool())

	response := callMCPTool(t, handler, automation.Identity{TokenID: "read", Role: "read"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_managed_resource_stats","arguments":{"projectId":"project","kind":"redis","resourceId":"redis"}}}`)
	if strings.Contains(response, `"isError":true`) || !strings.Contains(response, `\"operationsPerSecond\":25`) || redis.calls != 1 {
		t.Fatalf("live stats = %s calls=%d", response, redis.calls)
	}

	bound := "project"
	response = callMCPTool(t, handler, automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound},
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_managed_resource_stats","arguments":{"projectId":"other","kind":"redis","resourceId":"redis"}}}`)
	if !strings.Contains(response, `"isError":true`) || repository.redisCalls != 1 || history.calls != 0 {
		t.Fatalf("cross-project stats = %s redisCalls=%d historyCalls=%d", response, repository.redisCalls, history.calls)
	}

	response = callMCPTool(t, handler, automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound},
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_managed_resource_stats","arguments":{"projectId":"project","kind":"postgres","resourceId":"postgres","range":"1h"}}}`)
	if strings.Contains(response, `"isError":true`) ||
		!strings.Contains(response, `\"operationsPerSecond\":7`) ||
		history.calls != 1 || history.kind != "postgres" || history.rangeName != "1h" {
		t.Fatalf("history stats = %s history=%+v", response, history)
	}

	response = callMCPTool(t, handler, automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound},
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_managed_resource_stats","arguments":{"projectId":"project","kind":"bucket","resourceId":"store"}}}`)
	if !strings.Contains(response, `"code":-32602`) {
		t.Fatalf("invalid kind = %s", response)
	}
}
