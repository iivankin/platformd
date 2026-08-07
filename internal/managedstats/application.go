package managedstats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

func NewApplication(store Store, config Config) (*Application, error) {
	if store == nil || config.Postgres == nil || config.Redis == nil || config.ObjectStore == nil {
		return nil, errors.New("managed stats dependencies are incomplete")
	}
	if config.PersistInterval == 0 {
		config.PersistInterval = PersistInterval
	}
	if config.Retention == 0 {
		config.Retention = Retention
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.PersistInterval <= 0 || config.Retention < config.PersistInterval {
		return nil, errors.New("managed stats intervals or retention are invalid")
	}
	return &Application{
		store: store, postgres: config.Postgres, redis: config.Redis, objectStore: config.ObjectStore,
		persistInterval: config.PersistInterval, retention: config.Retention, now: config.Now,
		previousPostgres:   make(map[string]postgresCounters),
		previousRedis:      make(map[string]redisCounters),
		previousObjectStore: make(map[string]objectStoreCounters),
	}, nil
}

func (application *Application) Run(ctx context.Context, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}
	application.collect(ctx, onError)
	ticker := time.NewTicker(application.persistInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			application.collect(ctx, onError)
		}
	}
}

func (application *Application) collect(ctx context.Context, onError func(error)) {
	observedAt := application.now()
	samples := make([]state.ManagedStatSample, 0)
	nextPostgres := make(map[string]postgresCounters)
	nextRedis := make(map[string]redisCounters)
	nextObjectStore := make(map[string]objectStoreCounters)
	postgresListed := false
	redisListed := false
	objectStoreListed := false

	postgresResources, err := application.store.ManagedPostgresResources(ctx)
	if err != nil {
		onError(fmt.Errorf("list managed postgres resources: %w", err))
	} else {
		postgresListed = true
		for _, resource := range postgresResources {
			stats, statsErr := application.postgres(ctx, resource.ID)
			if statsErr != nil {
				onError(fmt.Errorf("postgres %s stats: %w", resource.ID, statsErr))
				if previous, ok := application.previousPostgres[resource.ID]; ok {
					nextPostgres[resource.ID] = previous
				}
				continue
			}
			sample, counters := postgresSample(stats, application.previousPostgres[resource.ID], observedAt)
			encoded, encodeErr := json.Marshal(sample)
			if encodeErr != nil {
				onError(fmt.Errorf("encode postgres %s sample: %w", resource.ID, encodeErr))
				if previous, ok := application.previousPostgres[resource.ID]; ok {
					nextPostgres[resource.ID] = previous
				}
				continue
			}
			samples = append(samples, state.ManagedStatSample{
				Kind: "postgres", ResourceID: resource.ID, ObservedAt: observedAt.UnixMilli(), MetricsJSON: string(encoded),
			})
			nextPostgres[resource.ID] = counters
		}
	}

	redisResources, err := application.store.ManagedRedisResources(ctx)
	if err != nil {
		onError(fmt.Errorf("list managed redis resources: %w", err))
	} else {
		redisListed = true
		for _, resource := range redisResources {
			stats, statsErr := application.redis(ctx, resource.ID)
			if statsErr != nil {
				onError(fmt.Errorf("redis %s stats: %w", resource.ID, statsErr))
				if previous, ok := application.previousRedis[resource.ID]; ok {
					nextRedis[resource.ID] = previous
				}
				continue
			}
			sample, counters := redisSample(stats, application.previousRedis[resource.ID], observedAt)
			encoded, encodeErr := json.Marshal(sample)
			if encodeErr != nil {
				onError(fmt.Errorf("encode redis %s sample: %w", resource.ID, encodeErr))
				if previous, ok := application.previousRedis[resource.ID]; ok {
					nextRedis[resource.ID] = previous
				}
				continue
			}
			samples = append(samples, state.ManagedStatSample{
				Kind: "redis", ResourceID: resource.ID, ObservedAt: observedAt.UnixMilli(), MetricsJSON: string(encoded),
			})
			nextRedis[resource.ID] = counters
		}
	}

	objectStores, err := application.store.ObjectStores(ctx)
	if err != nil {
		onError(fmt.Errorf("list object stores: %w", err))
	} else {
		objectStoreListed = true
		for _, resource := range objectStores {
			stats, statsErr := application.objectStore(ctx, resource.ID)
			if statsErr != nil {
				onError(fmt.Errorf("object store %s stats: %w", resource.ID, statsErr))
				if previous, ok := application.previousObjectStore[resource.ID]; ok {
					nextObjectStore[resource.ID] = previous
				}
				continue
			}
			sample, counters := objectStoreSample(stats, application.previousObjectStore[resource.ID], observedAt)
			encoded, encodeErr := json.Marshal(sample)
			if encodeErr != nil {
				onError(fmt.Errorf("encode object store %s sample: %w", resource.ID, encodeErr))
				if previous, ok := application.previousObjectStore[resource.ID]; ok {
					nextObjectStore[resource.ID] = previous
				}
				continue
			}
			samples = append(samples, state.ManagedStatSample{
				Kind: "object_store", ResourceID: resource.ID, ObservedAt: observedAt.UnixMilli(), MetricsJSON: string(encoded),
			})
			nextObjectStore[resource.ID] = counters
		}
	}

	cutoff := observedAt.Add(-application.retention).UnixMilli()
	if err := application.store.RecordManagedStatBatch(ctx, samples, cutoff); err != nil {
		onError(fmt.Errorf("record managed stat batch: %w", err))
		return
	}
	if postgresListed {
		application.previousPostgres = nextPostgres
	}
	if redisListed {
		application.previousRedis = nextRedis
	}
	if objectStoreListed {
		application.previousObjectStore = nextObjectStore
	}
}

func postgresSample(stats managedpostgres.Stats, previous postgresCounters, at time.Time) (map[string]any, postgresCounters) {
	blockSize := stats.BlockSizeBytes
	if blockSize <= 0 {
		blockSize = 8192
	}
	current := postgresCounters{
		at: at, xactCommit: stats.XactCommit, xactRollback: stats.XactRollback,
		tupReturned: stats.TupReturned, tupFetched: stats.TupFetched,
		tupInserted: stats.TupInserted, tupUpdated: stats.TupUpdated, tupDeleted: stats.TupDeleted,
		blksRead: stats.BlksRead, blksHit: stats.BlksHit, blockSize: blockSize,
		statementsCalls: stats.StatementsCalls, statementsExecMs: stats.StatementsTotalExecTimeMillis,
		statements: make(map[string]statementCounter, len(stats.Statements)+len(previous.statements)),
	}
	liveStatements := make(map[string]struct{}, len(stats.Statements))
	for _, statement := range stats.Statements {
		liveStatements[statement.QueryID] = struct{}{}
		current.statements[statement.QueryID] = statementCounter{
			query: statement.Query, calls: statement.Calls, exec: statement.TotalExecTimeMillis,
		}
	}
	// Keep baselines for statements that fell out of the top-N snapshot so a
	// later re-entry does not treat lifetime counters as an interval delta.
	for id, counter := range previous.statements {
		if _, ok := current.statements[id]; ok {
			continue
		}
		current.statements[id] = counter
	}
	if len(current.statements) > maxRetainedStatements {
		for id := range current.statements {
			if len(current.statements) <= maxRetainedStatements {
				break
			}
			if _, live := liveStatements[id]; !live {
				delete(current.statements, id)
			}
		}
	}
	sample := map[string]any{
		"connections": stats.Connections, "active": stats.Active,
		"idleInTransaction": stats.IdleInTransaction, "cacheHitPercent": stats.CacheHitPercent,
		"commitsPerSecond": 0.0, "rollbacksPerSecond": 0.0, "queriesPerSecond": 0.0, "queryCount": int64(0),
		"transactionsPerSecond": 0.0, "meanQueryLatencyMillis": 0.0,
		"rowsReadPerSecond": 0.0, "rowsWrittenPerSecond": 0.0, "rowsRead": int64(0), "rowsWritten": int64(0),
		"bytesReadPerSecond": 0.0, "bytesHitPerSecond": 0.0, "bytesRead": int64(0), "bytesHit": int64(0),
		"databaseSizeBytes": stats.DatabaseSizeBytes, "blockedCount": int64(len(stats.Blocked)),
		"topQueries": []TopQuerySample{}, "otherQueriesPerSecond": 0.0, "otherQueryCount": int64(0),
	}
	if previous.at.IsZero() || !at.After(previous.at) {
		return sample, current
	}
	seconds := at.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return sample, current
	}
	commitDelta := nonNegativeDelta(stats.XactCommit, previous.xactCommit)
	rollbackDelta := nonNegativeDelta(stats.XactRollback, previous.xactRollback)
	rowsRead := nonNegativeDelta(stats.TupReturned+stats.TupFetched, previous.tupReturned+previous.tupFetched)
	rowsWritten := nonNegativeDelta(
		stats.TupInserted+stats.TupUpdated+stats.TupDeleted,
		previous.tupInserted+previous.tupUpdated+previous.tupDeleted,
	)
	blksRead := nonNegativeDelta(stats.BlksRead, previous.blksRead)
	blksHit := nonNegativeDelta(stats.BlksHit, previous.blksHit)
	queryDelta := nonNegativeDelta(stats.StatementsCalls, previous.statementsCalls)
	execDelta := math.Max(0, stats.StatementsTotalExecTimeMillis-previous.statementsExecMs)

	sample["commitsPerSecond"] = float64(commitDelta) / seconds
	sample["rollbacksPerSecond"] = float64(rollbackDelta) / seconds
	sample["transactionsPerSecond"] = float64(commitDelta+rollbackDelta) / seconds
	sample["queriesPerSecond"] = float64(queryDelta) / seconds
	sample["queryCount"] = queryDelta
	if queryDelta > 0 {
		sample["meanQueryLatencyMillis"] = execDelta / float64(queryDelta)
	}
	sample["rowsReadPerSecond"] = float64(rowsRead) / seconds
	sample["rowsWrittenPerSecond"] = float64(rowsWritten) / seconds
	sample["rowsRead"] = rowsRead
	sample["rowsWritten"] = rowsWritten
	sample["bytesRead"] = blksRead * blockSize
	sample["bytesHit"] = blksHit * blockSize
	sample["bytesReadPerSecond"] = float64(blksRead*blockSize) / seconds
	sample["bytesHitPerSecond"] = float64(blksHit*blockSize) / seconds

	type rankedQuery struct {
		id, query     string
		calls         int64
		meanLatencyMs float64
	}
	ranked := make([]rankedQuery, 0, len(current.statements))
	var topCalls int64
	for id, counter := range current.statements {
		prior := previous.statements[id]
		deltaCalls := nonNegativeDelta(counter.calls, prior.calls)
		if deltaCalls <= 0 {
			continue
		}
		deltaExec := math.Max(0, counter.exec-prior.exec)
		ranked = append(ranked, rankedQuery{
			id: id, query: clipQuery(counter.query, queryClipLimit),
			calls: deltaCalls, meanLatencyMs: deltaExec / float64(deltaCalls),
		})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].calls == ranked[right].calls {
			return ranked[left].id < ranked[right].id
		}
		return ranked[left].calls > ranked[right].calls
	})
	top := make([]TopQuerySample, 0, topN)
	for index, item := range ranked {
		if index >= topN {
			break
		}
		topCalls += item.calls
		top = append(top, TopQuerySample{
			QueryID: item.id, Query: item.query,
			CallsPerSecond: float64(item.calls) / seconds, CallCount: item.calls,
			MeanLatencyMillis: item.meanLatencyMs,
		})
	}
	other := queryDelta - topCalls
	if other < 0 {
		other = 0
	}
	sample["topQueries"] = top
	sample["otherQueryCount"] = other
	sample["otherQueriesPerSecond"] = float64(other) / seconds
	return sample, current
}

func redisSample(stats managedredis.Stats, previous redisCounters, at time.Time) (map[string]any, redisCounters) {
	current := redisCounters{
		at: at, totalCommands: stats.TotalCommands,
		totalNetInput: stats.TotalNetInputBytes, totalNetOutput: stats.TotalNetOutputBytes,
		commands: make(map[string]commandCounter, len(stats.Commands)),
	}
	for _, command := range stats.Commands {
		current.commands[command.Name] = commandCounter{
			calls: command.Calls, total: command.TotalMicros,
			p50: command.P50Micros, p95: command.P95Micros, p99: command.P99Micros,
		}
	}
	hitRate := 0.0
	lookups := stats.KeyspaceHits + stats.KeyspaceMisses
	if lookups > 0 {
		hitRate = 100 * float64(stats.KeyspaceHits) / float64(lookups)
	}
	sample := map[string]any{
		"connectedClients": stats.ConnectedClients, "blockedClients": stats.BlockedClients,
		"operationsPerSecond": float64(stats.OperationsPerSecond), "commandCount": int64(0),
		"meanCommandLatencyMicros": 0.0,
		"latencyP50Micros":         stats.LatencyP50Micros, "latencyP95Micros": stats.LatencyP95Micros,
		"latencyP99Micros": stats.LatencyP99Micros, "hitRatePercent": hitRate,
		"usedMemoryBytes": stats.UsedMemoryBytes, "evictedKeys": stats.EvictedKeys,
		"fragmentationRatio":     stats.FragmentationRatio,
		"netInputBytesPerSecond": 0.0, "netOutputBytesPerSecond": 0.0,
		"netInputBytes": int64(0), "netOutputBytes": int64(0),
		"topCommands": []TopCommandSample{}, "otherCommandsPerSecond": 0.0, "otherCommandCount": int64(0),
	}
	if previous.at.IsZero() || !at.After(previous.at) {
		return sample, current
	}
	seconds := at.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return sample, current
	}
	commandDelta := nonNegativeDelta(stats.TotalCommands, previous.totalCommands)
	netIn := nonNegativeDelta(stats.TotalNetInputBytes, previous.totalNetInput)
	netOut := nonNegativeDelta(stats.TotalNetOutputBytes, previous.totalNetOutput)
	sample["commandCount"] = commandDelta
	sample["netInputBytes"] = netIn
	sample["netOutputBytes"] = netOut
	sample["netInputBytesPerSecond"] = float64(netIn) / seconds
	sample["netOutputBytesPerSecond"] = float64(netOut) / seconds
	if stats.OperationsPerSecond == 0 && commandDelta > 0 {
		sample["operationsPerSecond"] = float64(commandDelta) / seconds
	}

	type rankedCommand struct {
		name              string
		key               string
		calls             int64
		totalMicros       int64
		meanLatencyMicros float64
		p50, p95, p99     float64
	}
	merged := make(map[string]*rankedCommand, len(current.commands))
	var totalMicrosDelta int64
	for name, counter := range current.commands {
		prior := previous.commands[name]
		deltaCalls := nonNegativeDelta(counter.calls, prior.calls)
		if deltaCalls <= 0 {
			continue
		}
		deltaMicros := nonNegativeDelta(counter.total, prior.total)
		totalMicrosDelta += deltaMicros
		key := commandMetricKey(name)
		if key == "" {
			// Keep unsanitizable names out of topN so their volume stays in
			// otherCommandsPerSecond and remains visible on the breakdown chart.
			continue
		}
		if existing := merged[key]; existing != nil {
			priorCalls := existing.calls
			existing.calls += deltaCalls
			existing.totalMicros += deltaMicros
			existing.meanLatencyMicros = float64(existing.totalMicros) / float64(existing.calls)
			if deltaCalls > priorCalls {
				existing.name = name
				existing.p50, existing.p95, existing.p99 = counter.p50, counter.p95, counter.p99
			}
			continue
		}
		merged[key] = &rankedCommand{
			name: name, key: key, calls: deltaCalls, totalMicros: deltaMicros,
			meanLatencyMicros: float64(deltaMicros) / float64(deltaCalls),
			p50:               counter.p50, p95: counter.p95, p99: counter.p99,
		}
	}
	ranked := make([]*rankedCommand, 0, len(merged))
	for _, item := range merged {
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].calls == ranked[right].calls {
			return ranked[left].name < ranked[right].name
		}
		return ranked[left].calls > ranked[right].calls
	})
	top := make([]TopCommandSample, 0, topN)
	var topCalls int64
	for _, item := range ranked {
		if len(top) >= topN {
			break
		}
		topCalls += item.calls
		rate := float64(item.calls) / seconds
		top = append(top, TopCommandSample{
			Name: item.name, CallsPerSecond: rate, CallCount: item.calls,
			MeanLatencyMicros: item.meanLatencyMicros, P50Micros: item.p50, P95Micros: item.p95, P99Micros: item.p99,
		})
		sample[item.key] = rate
	}
	if commandDelta > 0 {
		sample["meanCommandLatencyMicros"] = float64(totalMicrosDelta) / float64(commandDelta)
	}
	other := commandDelta - topCalls
	if other < 0 {
		other = 0
	}
	sample["topCommands"] = top
	sample["otherCommandCount"] = other
	sample["otherCommandsPerSecond"] = float64(other) / seconds
	return sample, current
}

func objectStoreSample(stats objectstore.ObjectStoreStats, previous objectStoreCounters, at time.Time) (map[string]any, objectStoreCounters) {
	current := objectStoreCounters{at: at, ops: map[string]uint64{}}
	activeRequests := int64(0)
	if stats.Traffic != nil {
		current.bytesIn = stats.Traffic.BytesIn
		current.bytesOut = stats.Traffic.BytesOut
		current.errors = stats.Traffic.Errors
		current.totalLatencyMicros = stats.Traffic.TotalLatencyMicros
		activeRequests = stats.Traffic.ActiveRequests
		for name, calls := range stats.Traffic.Ops {
			current.ops[name] = calls
		}
	}
	sample := map[string]any{
		"objectCount": stats.ObjectCount, "totalBytes": stats.TotalBytes,
		"activeRequests": activeRequests,
		"getOperationsPerSecond": 0.0, "putOperationsPerSecond": 0.0,
		"deleteOperationsPerSecond": 0.0, "listOperationsPerSecond": 0.0,
		"otherOperationsPerSecond": 0.0,
	}
	if previous.at.IsZero() || !at.After(previous.at) {
		sample["operationsPerSecond"] = 0.0
		sample["operationCount"] = int64(0)
		sample["bytesInPerSecond"] = 0.0
		sample["bytesOutPerSecond"] = 0.0
		sample["bytesIn"] = int64(0)
		sample["bytesOut"] = int64(0)
		sample["errorsPerSecond"] = 0.0
		sample["errorCount"] = int64(0)
		sample["meanLatencyMicros"] = 0.0
		sample["topOps"] = []TopOpSample{}
		return sample, current
	}
	seconds := at.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return sample, current
	}
	var opDelta uint64
	type rankedOp struct {
		name  string
		delta uint64
	}
	ranked := make([]rankedOp, 0, len(current.ops))
	for name, calls := range current.ops {
		delta := calls - previous.ops[name]
		if calls < previous.ops[name] {
			delta = 0
		}
		opDelta += delta
		if delta > 0 {
			ranked = append(ranked, rankedOp{name: name, delta: delta})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].delta > ranked[j].delta })
	top := make([]TopOpSample, 0, topN)
	for index, item := range ranked {
		if index >= topN {
			break
		}
		top = append(top, TopOpSample{
			Name: item.name, CallsPerSecond: float64(item.delta) / seconds, CallCount: int64(item.delta),
		})
	}
	for _, name := range []string{"get", "put", "delete", "list", "other"} {
		delta := current.ops[name] - previous.ops[name]
		if current.ops[name] < previous.ops[name] {
			delta = 0
		}
		sample[name+"OperationsPerSecond"] = float64(delta) / seconds
		if name == "other" {
			sample["otherOperationCount"] = int64(delta)
		}
	}
	bytesIn := current.bytesIn - previous.bytesIn
	if current.bytesIn < previous.bytesIn {
		bytesIn = 0
	}
	bytesOut := current.bytesOut - previous.bytesOut
	if current.bytesOut < previous.bytesOut {
		bytesOut = 0
	}
	errors := current.errors - previous.errors
	if current.errors < previous.errors {
		errors = 0
	}
	latencyDelta := current.totalLatencyMicros - previous.totalLatencyMicros
	if current.totalLatencyMicros < previous.totalLatencyMicros {
		latencyDelta = 0
	}
	sample["operationsPerSecond"] = float64(opDelta) / seconds
	sample["operationCount"] = int64(opDelta)
	sample["bytesInPerSecond"] = float64(bytesIn) / seconds
	sample["bytesOutPerSecond"] = float64(bytesOut) / seconds
	sample["bytesIn"] = int64(bytesIn)
	sample["bytesOut"] = int64(bytesOut)
	sample["errorsPerSecond"] = float64(errors) / seconds
	sample["errorCount"] = int64(errors)
	if opDelta > 0 {
		sample["meanLatencyMicros"] = float64(latencyDelta) / float64(opDelta)
	} else {
		sample["meanLatencyMicros"] = 0.0
	}
	sample["topOps"] = top
	return sample, current
}

func commandMetricKey(name string) string {
	sanitized := make([]byte, 0, len(name))
	for index := 0; index < len(name); index++ {
		char := name[index]
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_':
			sanitized = append(sanitized, char)
		case char >= 'A' && char <= 'Z':
			sanitized = append(sanitized, char+'a'-'A')
		}
	}
	if len(sanitized) == 0 {
		return ""
	}
	return "cmd." + string(sanitized)
}

func nonNegativeDelta(current, previous int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func clipQuery(query string, limit int) string {
	if limit <= 0 {
		return query
	}
	value := []rune(query)
	if len(value) <= limit {
		return query
	}
	return string(value[:limit])
}
