package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/managedstats"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type managedStatsStoreStub struct {
	samples []state.ManagedStatSample
}

func (*managedStatsStoreStub) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	return nil, nil
}
func (*managedStatsStoreStub) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return nil, nil
}
func (*managedStatsStoreStub) ObjectStores(context.Context) ([]state.ObjectStore, error) {
	return nil, nil
}
func (*managedStatsStoreStub) RecordManagedStatBatch(context.Context, []state.ManagedStatSample, int64) error {
	return nil
}
func (store *managedStatsStoreStub) ManagedStatSamples(_ context.Context, kind, resourceID string, from, to int64) ([]state.ManagedStatSample, error) {
	result := make([]state.ManagedStatSample, 0)
	for _, sample := range store.samples {
		if sample.Kind == kind && sample.ResourceID == resourceID && sample.ObservedAt >= from && sample.ObservedAt <= to {
			result = append(result, sample)
		}
	}
	return result, nil
}

func TestManagedPostgresStatsHistoryRequiresAccessAndValidatesRange(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(3_600_000)
	metrics, err := json.Marshal(map[string]any{"queriesPerSecond": 12.0, "queryCount": 12})
	if err != nil {
		t.Fatal(err)
	}
	statsStore := &managedStatsStoreStub{samples: []state.ManagedStatSample{{
		Kind: "postgres", ResourceID: "postgres", ObservedAt: now.UnixMilli() - 30_000, MetricsJSON: string(metrics),
	}}}
	stats, err := managedstats.NewApplication(statsStore, managedstats.Config{
		Now:      func() time.Time { return now },
		Postgres: func(context.Context, string) (managedpostgres.Stats, error) { return managedpostgres.Stats{}, nil },
		Redis:    func(context.Context, string) (managedredis.Stats, error) { return managedredis.Stats{}, nil },
		ObjectStore: func(context.Context, string) (objectstore.ObjectStoreStats, error) {
			return objectstore.ObjectStoreStats{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	postgresStore := &postgresStoreStub{resource: state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
	}}
	application, err := managedpostgres.NewApplication(
		context.Background(), postgresStore, &postgresRuntimeStub{}, cryptobox.MasterKey{}, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	direct := server.Handler(
		server.DefaultMeta("ready"),
		server.WithManagedPostgres(application),
		server.WithManagedStats(stats),
	)
	path := "/api/v1/projects/project/postgres/postgres/stats/history?range=1h"
	unauthenticated := httptest.NewRecorder()
	direct.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthenticated.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated history = %d/%s", unauthenticated.Code, unauthenticated.Body)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, projectRequest(http.MethodGet, path, ""))
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"stepMillis":60000`) ||
		!strings.Contains(ok.Body.String(), `"queriesPerSecond":12`) ||
		!strings.Contains(ok.Body.String(), `"queryCount":12`) {
		t.Fatalf("history = %d/%s", ok.Code, ok.Body)
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, projectRequest(http.MethodGet, "/api/v1/projects/project/postgres/postgres/stats/history?range=2h", ""))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_managed_stats_range") {
		t.Fatalf("invalid range = %d/%s", invalid.Code, invalid.Body)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, projectRequest(http.MethodGet, "/api/v1/projects/other/postgres/postgres/stats/history?range=1h", ""))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("cross-project history = %d/%s", missing.Code, missing.Body)
	}
}
