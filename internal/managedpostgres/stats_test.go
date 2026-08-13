package managedpostgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTruncateQuery(t *testing.T) {
	if got := truncateQuery("short", 500); got != "short" {
		t.Fatalf("truncateQuery short = %q", got)
	}
	got := truncateQuery(strings.Repeat("a", 600), 500)
	if len([]rune(got)) != 500 {
		t.Fatalf("truncateQuery len = %d", len([]rune(got)))
	}
	if got := truncateQuery(strings.Repeat("世", 10), 3); got != "世世世" {
		t.Fatalf("truncateQuery runes = %q", got)
	}
	if got := truncateQuery("abc", 0); got != "abc" {
		t.Fatalf("truncateQuery zero limit = %q", got)
	}
}

func TestRecordStatementStatSkipsNullQueryID(t *testing.T) {
	t.Parallel()
	stats := Stats{Statements: make([]StatementStat, 0)}
	recordStatementStat(&stats, StatementStat{QueryID: "", Query: "SET", Calls: 3}, 40, 12)
	if stats.StatementsCalls != 40 || stats.StatementsTotalExecTimeMillis != 12 || stats.MeanQueryLatencyMillis != 0.3 {
		t.Fatalf("window totals = calls=%d exec=%v mean=%v", stats.StatementsCalls, stats.StatementsTotalExecTimeMillis, stats.MeanQueryLatencyMillis)
	}
	if len(stats.Statements) != 0 {
		t.Fatalf("null queryid must not appear in statements: %+v", stats.Statements)
	}
	recordStatementStat(&stats, StatementStat{QueryID: "42", Query: strings.Repeat("a", 600), Calls: 7}, 40, 12)
	if len(stats.Statements) != 1 || stats.Statements[0].QueryID != "42" || len([]rune(stats.Statements[0].Query)) != 500 {
		t.Fatalf("statement = %+v", stats.Statements)
	}
}

func TestStatsAgainstRealPostgresWithHiddenSessions(t *testing.T) {
	connectionURL := os.Getenv("PLATFORMD_MANAGED_POSTGRES_TEST_URL")
	if connectionURL == "" {
		t.Skip("set PLATFORMD_MANAGED_POSTGRES_TEST_URL to a non-superuser PostgreSQL connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{connection: connection}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	var hiddenSessions int
	if err := connection.QueryRow(ctx, `
SELECT count(*)
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND xact_start IS NULL
  AND query_start IS NULL
  AND backend_start IS NULL`).Scan(&hiddenSessions); err != nil {
		t.Fatal(err)
	}
	if hiddenSessions == 0 {
		t.Skip("database user can inspect every session; a non-superuser is required")
	}
	stats, err := client.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Sessions) < hiddenSessions {
		t.Fatalf("sessions = %d, hidden sessions = %d", len(stats.Sessions), hiddenSessions)
	}
	for _, session := range stats.Sessions {
		if session.DurationMillis < 0 {
			t.Fatalf("negative session duration: %+v", session)
		}
	}
}

func TestEnrichStatsRatesFromPreviousSnapshot(t *testing.T) {
	t.Parallel()
	previous := statsRateSnapshot{
		at: time.UnixMilli(0), xactCommit: 10, xactRollback: 2,
		tupReturned: 100, tupFetched: 50, tupInserted: 5, tupUpdated: 2, tupDeleted: 1,
		blksRead: 4, blksHit: 20, blockSize: 8192, statementsCalls: 100,
	}
	stats := Stats{
		XactCommit: 70, XactRollback: 5,
		TupReturned: 300, TupFetched: 100, TupInserted: 15, TupUpdated: 7, TupDeleted: 3,
		BlksRead: 14, BlksHit: 40, BlockSizeBytes: 8192, StatementsCalls: 160,
	}
	current := enrichStatsRates(&stats, previous, time.UnixMilli(60_000))
	if stats.QueriesPerSecond != 1 || stats.TransactionsPerSecond != 63.0/60 {
		t.Fatalf("query/txn rates = qps=%v tps=%v", stats.QueriesPerSecond, stats.TransactionsPerSecond)
	}
	if stats.RowsReadPerSecond != 250.0/60 || stats.RowsWrittenPerSecond != 17.0/60 {
		t.Fatalf("row rates = read=%v write=%v", stats.RowsReadPerSecond, stats.RowsWrittenPerSecond)
	}
	if stats.BytesReadPerSecond != float64(10*8192)/60 || stats.BytesHitPerSecond != float64(20*8192)/60 {
		t.Fatalf("byte rates = read=%v hit=%v", stats.BytesReadPerSecond, stats.BytesHitPerSecond)
	}
	if current.statementsCalls != 160 || current.at.UnixMilli() != 60_000 {
		t.Fatalf("snapshot = %+v", current)
	}
}
