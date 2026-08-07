package managedstats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

type storeStub struct {
	postgres []state.ManagedPostgres
	redis    []state.ManagedRedis
	objects  []state.ObjectStore
	samples  []state.ManagedStatSample
	cutoff   int64
}

func (store *storeStub) ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error) {
	return store.postgres, nil
}
func (store *storeStub) ManagedRedisResources(context.Context) ([]state.ManagedRedis, error) {
	return store.redis, nil
}
func (store *storeStub) ObjectStores(context.Context) ([]state.ObjectStore, error) {
	return store.objects, nil
}
func (store *storeStub) RecordManagedStatBatch(_ context.Context, samples []state.ManagedStatSample, cutoff int64) error {
	store.samples = append(store.samples, samples...)
	store.cutoff = cutoff
	return nil
}
func (store *storeStub) ManagedStatSamples(_ context.Context, kind, resourceID string, from, to int64) ([]state.ManagedStatSample, error) {
	result := make([]state.ManagedStatSample, 0)
	for _, sample := range store.samples {
		if sample.Kind == kind && sample.ResourceID == resourceID && sample.ObservedAt >= from && sample.ObservedAt <= to {
			result = append(result, sample)
		}
	}
	return result, nil
}

func TestPostgresSampleComputesDeltasAndTopQueries(t *testing.T) {
	t.Parallel()
	previous := postgresCounters{
		at: time.UnixMilli(0), xactCommit: 10, xactRollback: 1,
		tupReturned: 100, tupFetched: 50, tupInserted: 5, tupUpdated: 2, tupDeleted: 1,
		blksRead: 4, blksHit: 20, blockSize: 8192,
		statementsCalls: 100, statementsExecMs: 200,
		statements: map[string]statementCounter{
			"q1": {query: "SELECT 1", calls: 40, exec: 80},
			"q2": {query: "SELECT 2", calls: 30, exec: 60},
			"q3": {query: "SELECT 3", calls: 20, exec: 40},
		},
	}
	stats := managedpostgres.Stats{
		Connections: 3, Active: 1, IdleInTransaction: 1, CacheHitPercent: 90,
		XactCommit: 70, XactRollback: 3,
		TupReturned: 300, TupFetched: 100, TupInserted: 15, TupUpdated: 7, TupDeleted: 3,
		BlksRead: 14, BlksHit: 40, BlockSizeBytes: 8192,
		StatementsCalls: 180, StatementsTotalExecTimeMillis: 360,
		Blocked: []managedpostgres.BlockedStat{{BlockedPID: 1}},
		Statements: []managedpostgres.StatementStat{
			{QueryID: "q1", Query: "SELECT 1", Calls: 90, TotalExecTimeMillis: 180},
			{QueryID: "q2", Query: "SELECT 2", Calls: 50, TotalExecTimeMillis: 100},
			{QueryID: "q3", Query: "SELECT 3", Calls: 30, TotalExecTimeMillis: 60},
			{QueryID: "q4", Query: "SELECT 4", Calls: 10, TotalExecTimeMillis: 20},
		},
	}
	sample, _ := postgresSample(stats, previous, time.UnixMilli(60_000))
	if sample["commitsPerSecond"].(float64) != 1 || sample["queryCount"].(int64) != 80 {
		t.Fatalf("rates = %+v", sample)
	}
	if sample["rowsRead"].(int64) != 250 || sample["bytesRead"].(int64) != 10*8192 {
		t.Fatalf("counts = %+v", sample)
	}
	top := sample["topQueries"].([]TopQuerySample)
	if len(top) == 0 || top[0].QueryID != "q1" || top[0].CallCount != 50 {
		t.Fatalf("top queries = %+v", top)
	}
	if sample["blockedCount"].(int64) != 1 || sample["otherQueryCount"].(int64) != 0 {
		t.Fatalf("other/blocked = %+v", sample)
	}
}

func TestPostgresSamplePreservesBaselinesAcrossTopNChurn(t *testing.T) {
	t.Parallel()
	// q1 is in the first snapshot, then drops out, then returns with more calls.
	first := managedpostgres.Stats{
		StatementsCalls: 100, StatementsTotalExecTimeMillis: 100,
		Statements: []managedpostgres.StatementStat{
			{QueryID: "q1", Query: "SELECT 1", Calls: 100, TotalExecTimeMillis: 100},
		},
	}
	_, mid := postgresSample(first, postgresCounters{}, time.UnixMilli(60_000))
	withoutQ1 := managedpostgres.Stats{
		StatementsCalls: 150, StatementsTotalExecTimeMillis: 150,
		Statements: []managedpostgres.StatementStat{
			{QueryID: "q2", Query: "SELECT 2", Calls: 50, TotalExecTimeMillis: 50},
		},
	}
	_, preserved := postgresSample(withoutQ1, mid, time.UnixMilli(120_000))
	if preserved.statements["q1"].calls != 100 {
		t.Fatalf("expected retained q1 baseline, got %+v", preserved.statements["q1"])
	}
	reentered := managedpostgres.Stats{
		StatementsCalls: 210, StatementsTotalExecTimeMillis: 210,
		Statements: []managedpostgres.StatementStat{
			{QueryID: "q1", Query: "SELECT 1", Calls: 130, TotalExecTimeMillis: 130},
			{QueryID: "q2", Query: "SELECT 2", Calls: 80, TotalExecTimeMillis: 80},
		},
	}
	sample, _ := postgresSample(reentered, preserved, time.UnixMilli(180_000))
	top := sample["topQueries"].([]TopQuerySample)
	var q1 TopQuerySample
	for _, item := range top {
		if item.QueryID == "q1" {
			q1 = item
		}
	}
	if q1.CallCount != 30 {
		t.Fatalf("q1 interval calls = %d, want 30 (not lifetime 130); top=%+v", q1.CallCount, top)
	}
	if sample["otherQueryCount"].(int64) != 0 {
		t.Fatalf("otherQueryCount = %+v", sample["otherQueryCount"])
	}
}

func TestHistoryBucketsAverageRatesAndSumCounts(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	now := time.UnixMilli(3_600_000)
	application, err := NewApplication(store, Config{
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
	metricsA, _ := json.Marshal(map[string]any{"queriesPerSecond": 10.0, "queryCount": 10})
	metricsB, _ := json.Marshal(map[string]any{"queriesPerSecond": 20.0, "queryCount": 20})
	store.samples = []state.ManagedStatSample{
		{Kind: "postgres", ResourceID: "db", ObservedAt: now.UnixMilli() - 90_000, MetricsJSON: string(metricsA)},
		{Kind: "postgres", ResourceID: "db", ObservedAt: now.UnixMilli() - 10_000, MetricsJSON: string(metricsB)},
	}
	history, err := application.History(context.Background(), "postgres", "db", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if history.StepMillis != time.Minute.Milliseconds() || len(history.Points) != 2 {
		t.Fatalf("history = %+v", history)
	}
	if history.Totals.QueryCount != 30 {
		t.Fatalf("totals = %+v", history.Totals)
	}
	first := history.Points[0].Metrics["queriesPerSecond"].(float64)
	if first != 10 {
		t.Fatalf("first bucket rate = %v", first)
	}
}

func TestRedisSampleUsesCommandDeltas(t *testing.T) {
	t.Parallel()
	previous := redisCounters{
		at: time.UnixMilli(0), totalCommands: 100,
		totalNetInput: 1000, totalNetOutput: 2000,
		commands: map[string]commandCounter{"get": {calls: 40, total: 400}, "set": {calls: 20, total: 200}},
	}
	stats := managedredis.Stats{
		ConnectedClients: 2, OperationsPerSecond: 0, TotalCommands: 160,
		TotalNetInputBytes: 1600, TotalNetOutputBytes: 2600,
		KeyspaceHits: 90, KeyspaceMisses: 10, LatencyP50Micros: 11,
		Commands: []managedredis.CommandStat{
			{Name: "get", Calls: 80, TotalMicros: 800, P50Micros: 10},
			{Name: "set", Calls: 30, TotalMicros: 300, P99Micros: 40},
			{Name: "del", Calls: 10, TotalMicros: 100},
		},
	}
	sample, _ := redisSample(stats, previous, time.UnixMilli(60_000))
	if sample["commandCount"].(int64) != 60 || sample["operationsPerSecond"].(float64) != 1 {
		t.Fatalf("redis rates = %+v", sample)
	}
	top := sample["topCommands"].([]TopCommandSample)
	if len(top) == 0 || top[0].Name != "get" || top[0].CallCount != 40 {
		t.Fatalf("top commands = %+v", top)
	}
	if sample["cmd.get"].(float64) != 40.0/60 || sample["cmd.set"].(float64) != 10.0/60 {
		t.Fatalf("cmd rates = %+v", sample)
	}
	if sample["otherCommandsPerSecond"].(float64) != 0 {
		t.Fatalf("other commands = %+v", sample)
	}
}

func TestObjectStoreSampleFlattensOpRates(t *testing.T) {
	t.Parallel()
	previous := objectStoreCounters{
		at: time.UnixMilli(0),
		ops: map[string]uint64{
			"get": 100, "put": 40, "delete": 10, "list": 20, "other": 5,
		},
		bytesIn: 1000, bytesOut: 2000, errors: 1, totalLatencyMicros: 50_000,
	}
	stats := objectstore.ObjectStoreStats{
		ObjectCount: 12, TotalBytes: 4096,
		Traffic: &objectstore.ObjectStoreTraffic{
			Ops: map[string]uint64{
				"get": 160, "put": 55, "delete": 12, "list": 26, "other": 8,
			},
			BytesIn: 1600, BytesOut: 2600, Errors: 2, TotalLatencyMicros: 80_000,
		},
	}
	sample, _ := objectStoreSample(stats, previous, time.UnixMilli(60_000))
	if sample["operationsPerSecond"].(float64) != 86.0/60 {
		t.Fatalf("ops/s = %+v", sample["operationsPerSecond"])
	}
	if sample["getOperationsPerSecond"].(float64) != 1 ||
		sample["putOperationsPerSecond"].(float64) != 15.0/60 ||
		sample["deleteOperationsPerSecond"].(float64) != 2.0/60 ||
		sample["listOperationsPerSecond"].(float64) != 6.0/60 ||
		sample["otherOperationsPerSecond"].(float64) != 3.0/60 {
		t.Fatalf("op rates = %+v", sample)
	}
}

func TestCommandMetricKey(t *testing.T) {
	t.Parallel()
	if got := commandMetricKey("GET"); got != "cmd.get" {
		t.Fatalf("GET -> %q", got)
	}
	if got := commandMetricKey("xadd-!"); got != "cmd.xadd" {
		t.Fatalf("xadd-! -> %q", got)
	}
	if got := commandMetricKey("!!!"); got != "" {
		t.Fatalf("!!! -> %q", got)
	}
}

func TestRedisSampleSkipsUnsanitizableCommandNames(t *testing.T) {
	t.Parallel()
	previous := redisCounters{
		at: time.UnixMilli(0), totalCommands: 10,
		commands: map[string]commandCounter{
			"!!!": {calls: 10, total: 100},
			"get": {calls: 0, total: 0},
		},
	}
	stats := managedredis.Stats{
		TotalCommands: 40,
		Commands: []managedredis.CommandStat{
			{Name: "!!!", Calls: 30, TotalMicros: 300},
			{Name: "get", Calls: 10, TotalMicros: 100},
		},
	}
	sample, _ := redisSample(stats, previous, time.UnixMilli(60_000))
	if _, exists := sample["cmd."]; exists {
		t.Fatalf("unexpected empty cmd key: %+v", sample)
	}
	if sample["cmd.get"].(float64) != 10.0/60 {
		t.Fatalf("cmd.get = %+v", sample["cmd.get"])
	}
	// Unsanitizable volume should remain in Other, not disappear.
	if sample["otherCommandsPerSecond"].(float64) != 20.0/60 {
		t.Fatalf("other = %+v", sample["otherCommandsPerSecond"])
	}
}

func TestRedisSampleMergesSanitizedCommandKeyCollisions(t *testing.T) {
	t.Parallel()
	previous := redisCounters{
		at: time.UnixMilli(0), totalCommands: 0,
		commands: map[string]commandCounter{},
	}
	stats := managedredis.Stats{
		TotalCommands: 30,
		Commands: []managedredis.CommandStat{
			{Name: "xadd", Calls: 10, TotalMicros: 100},
			{Name: "xadd-!", Calls: 20, TotalMicros: 200},
		},
	}
	sample, _ := redisSample(stats, previous, time.UnixMilli(60_000))
	if sample["cmd.xadd"].(float64) != 30.0/60 {
		t.Fatalf("cmd.xadd = %+v", sample["cmd.xadd"])
	}
	if sample["otherCommandsPerSecond"].(float64) != 0 {
		t.Fatalf("other = %+v", sample["otherCommandsPerSecond"])
	}
	top, ok := sample["topCommands"].([]TopCommandSample)
	if !ok || len(top) != 1 || top[0].Name != "xadd-!" || top[0].CallCount != 30 {
		t.Fatalf("topCommands = %+v", sample["topCommands"])
	}
}

func TestHistoryBucketZeroFillsSparseRates(t *testing.T) {
	t.Parallel()
	bucket := &historyBucket{values: make(map[string]*bucketValue)}
	bucket.add(map[string]any{"cmd.get": 10.0, "otherCommandsPerSecond": 1.0})
	bucket.add(map[string]any{"otherCommandsPerSecond": 1.0})
	metrics := bucket.finish()
	if metrics["cmd.get"].(float64) != 5.0 {
		t.Fatalf("sparse cmd.get averaged to %+v, want 5", metrics["cmd.get"])
	}
	if metrics["otherCommandsPerSecond"].(float64) != 1.0 {
		t.Fatalf("other = %+v", metrics["otherCommandsPerSecond"])
	}
}

func TestCollectPreservesPreviousCountersOnStatsError(t *testing.T) {
	t.Parallel()
	store := &storeStub{
		postgres: []state.ManagedPostgres{{ID: "db"}},
	}
	calls := 0
	application, err := NewApplication(store, Config{
		Now: func() time.Time { return time.UnixMilli(120_000) },
		Postgres: func(context.Context, string) (managedpostgres.Stats, error) {
			calls++
			if calls == 1 {
				return managedpostgres.Stats{
					XactCommit: 10, StatementsCalls: 20, BlockSizeBytes: 8192,
				}, nil
			}
			return managedpostgres.Stats{}, context.DeadlineExceeded
		},
		Redis: func(context.Context, string) (managedredis.Stats, error) { return managedredis.Stats{}, nil },
		ObjectStore: func(context.Context, string) (objectstore.ObjectStoreStats, error) {
			return objectstore.ObjectStoreStats{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(error) {})
	if len(application.previousPostgres) != 1 || application.previousPostgres["db"].statementsCalls != 20 {
		t.Fatalf("first previous = %+v", application.previousPostgres)
	}
	application.now = func() time.Time { return time.UnixMilli(180_000) }
	application.collect(context.Background(), func(error) {})
	if len(application.previousPostgres) != 1 || application.previousPostgres["db"].statementsCalls != 20 {
		t.Fatalf("preserved previous = %+v", application.previousPostgres)
	}
	if len(store.samples) != 1 {
		t.Fatalf("samples after error = %d", len(store.samples))
	}
}
