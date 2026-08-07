package managedpostgres

import (
	"strings"
	"testing"
	"time"
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
