package managedstats

import (
	"context"
	"errors"
	"time"

	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

const (
	PersistInterval = time.Minute
	Retention       = 30 * 24 * time.Hour
	// Matches the Operations chart: up to 7 cmd.* series + Other.
	topN                  = 7
	queryClipLimit        = 120
	maxRetainedStatements = 128
)

var (
	ErrInvalidRange  = errors.New("invalid managed stats range")
	ErrInvalidKind   = errors.New("invalid managed stats kind")
	ErrInvalidTarget = errors.New("invalid managed stats target")
)

type Store interface {
	ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error)
	ManagedRedisResources(context.Context) ([]state.ManagedRedis, error)
	ObjectStores(context.Context) ([]state.ObjectStore, error)
	RecordManagedStatBatch(context.Context, []state.ManagedStatSample, int64) error
	ManagedStatSamples(context.Context, string, string, int64, int64) ([]state.ManagedStatSample, error)
}

type PostgresStats func(context.Context, string) (managedpostgres.Stats, error)
type RedisStats func(context.Context, string) (managedredis.Stats, error)
type ObjectStoreStats func(context.Context, string) (objectstore.ObjectStoreStats, error)

type Config struct {
	PersistInterval time.Duration
	Retention       time.Duration
	Now             func() time.Time
	Postgres        PostgresStats
	Redis           RedisStats
	ObjectStore     ObjectStoreStats
}

type History struct {
	From       int64   `json:"from"`
	To         int64   `json:"to"`
	StepMillis int64   `json:"stepMillis"`
	Points     []Point `json:"points"`
	Totals     Totals  `json:"totals"`
}

type Point struct {
	ObservedAt int64          `json:"observedAt"`
	Metrics    map[string]any `json:"metrics"`
}

type Totals struct {
	QueryCount      float64 `json:"queryCount,omitempty"`
	RowsRead        float64 `json:"rowsRead,omitempty"`
	RowsWritten     float64 `json:"rowsWritten,omitempty"`
	BytesRead       float64 `json:"bytesRead,omitempty"`
	BytesHit        float64 `json:"bytesHit,omitempty"`
	CommandCount    float64 `json:"commandCount,omitempty"`
	NetInputBytes   float64 `json:"netInputBytes,omitempty"`
	NetOutputBytes  float64 `json:"netOutputBytes,omitempty"`
	OperationCount  float64 `json:"operationCount,omitempty"`
	BytesIn         float64 `json:"bytesIn,omitempty"`
	BytesOut        float64 `json:"bytesOut,omitempty"`
	ErrorCount      float64 `json:"errorCount,omitempty"`
	OtherQueryCount float64 `json:"otherQueryCount,omitempty"`
}

type TopQuerySample struct {
	QueryID           string  `json:"queryId"`
	Query             string  `json:"query"`
	CallsPerSecond    float64 `json:"callsPerSecond"`
	CallCount         int64   `json:"callCount"`
	MeanLatencyMillis float64 `json:"meanLatencyMillis"`
}

type TopCommandSample struct {
	Name              string  `json:"name"`
	CallsPerSecond    float64 `json:"callsPerSecond"`
	CallCount         int64   `json:"callCount"`
	MeanLatencyMicros float64 `json:"meanLatencyMicros"`
	P50Micros         float64 `json:"p50Micros"`
	P95Micros         float64 `json:"p95Micros"`
	P99Micros         float64 `json:"p99Micros"`
}

type TopOpSample struct {
	Name           string  `json:"name"`
	CallsPerSecond float64 `json:"callsPerSecond"`
	CallCount      int64   `json:"callCount"`
}

type Application struct {
	store           Store
	postgres        PostgresStats
	redis           RedisStats
	objectStore     ObjectStoreStats
	persistInterval time.Duration
	retention       time.Duration
	now             func() time.Time

	previousPostgres    map[string]postgresCounters
	previousRedis       map[string]redisCounters
	previousObjectStore map[string]objectStoreCounters
}

type postgresCounters struct {
	at               time.Time
	xactCommit       int64
	xactRollback     int64
	tupReturned      int64
	tupFetched       int64
	tupInserted      int64
	tupUpdated       int64
	tupDeleted       int64
	blksRead         int64
	blksHit          int64
	blockSize        int64
	statementsCalls  int64
	statementsExecMs float64
	statements       map[string]statementCounter
}

type statementCounter struct {
	query string
	calls int64
	exec  float64
}

type redisCounters struct {
	at             time.Time
	totalCommands  int64
	totalNetInput  int64
	totalNetOutput int64
	commands       map[string]commandCounter
}

type commandCounter struct {
	calls int64
	total int64
	p50   float64
	p95   float64
	p99   float64
}

type objectStoreCounters struct {
	at                 time.Time
	ops                map[string]uint64
	bytesIn            uint64
	bytesOut           uint64
	errors             uint64
	totalLatencyMicros uint64
}
