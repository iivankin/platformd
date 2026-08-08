package managedpostgres

import (
	"context"
	"fmt"
	"time"
)

// statsRateSnapshot holds absolute counters used to derive per-second rates
// between successive live Stats() calls for the same resource.
type statsRateSnapshot struct {
	at              time.Time
	xactCommit      int64
	xactRollback    int64
	tupReturned     int64
	tupFetched      int64
	tupInserted     int64
	tupUpdated      int64
	tupDeleted      int64
	blksRead        int64
	blksHit         int64
	blockSize       int64
	statementsCalls int64
}

func snapshotFromStats(stats Stats, at time.Time) statsRateSnapshot {
	blockSize := stats.BlockSizeBytes
	if blockSize <= 0 {
		blockSize = 8192
	}
	return statsRateSnapshot{
		at: at, xactCommit: stats.XactCommit, xactRollback: stats.XactRollback,
		tupReturned: stats.TupReturned, tupFetched: stats.TupFetched,
		tupInserted: stats.TupInserted, tupUpdated: stats.TupUpdated, tupDeleted: stats.TupDeleted,
		blksRead: stats.BlksRead, blksHit: stats.BlksHit, blockSize: blockSize,
		statementsCalls: stats.StatementsCalls,
	}
}

func enrichStatsRates(stats *Stats, previous statsRateSnapshot, at time.Time) statsRateSnapshot {
	current := snapshotFromStats(*stats, at)
	if previous.at.IsZero() || !at.After(previous.at) {
		return current
	}
	seconds := at.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return current
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

	stats.TransactionsPerSecond = float64(commitDelta+rollbackDelta) / seconds
	stats.QueriesPerSecond = float64(queryDelta) / seconds
	stats.RowsReadPerSecond = float64(rowsRead) / seconds
	stats.RowsWrittenPerSecond = float64(rowsWritten) / seconds
	stats.BytesReadPerSecond = float64(blksRead*current.blockSize) / seconds
	stats.BytesHitPerSecond = float64(blksHit*current.blockSize) / seconds
	return current
}

func nonNegativeDelta(current, previous int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

type SessionStat struct {
	PID            int32   `json:"pid"`
	Usename        string  `json:"usename"`
	State          string  `json:"state"`
	WaitEvent      string  `json:"waitEvent"`
	DurationMillis float64 `json:"durationMillis"`
	Query          string  `json:"query"`
	ClientAddr     string  `json:"clientAddr"`
}

type StatementStat struct {
	QueryID             string  `json:"queryId"`
	Query               string  `json:"query"`
	Calls               int64   `json:"calls"`
	TotalExecTimeMillis float64 `json:"totalExecTimeMillis"`
	MeanExecTimeMillis  float64 `json:"meanExecTimeMillis"`
	MaxExecTimeMillis   float64 `json:"maxExecTimeMillis"`
	Rows                int64   `json:"rows"`
	SharedBlksHit       int64   `json:"sharedBlksHit"`
	SharedBlksRead      int64   `json:"sharedBlksRead"`
	TempBlksRead        int64   `json:"tempBlksRead"`
	TempBlksWritten     int64   `json:"tempBlksWritten"`
	PercentOfTotalTime  float64 `json:"percentOfTotalTime"`
}

type IndexStat struct {
	Schema      string `json:"schema"`
	Table       string `json:"table"`
	Index       string `json:"index"`
	IdxScan     int64  `json:"idxScan"`
	IdxTupRead  int64  `json:"idxTupRead"`
	IdxTupFetch int64  `json:"idxTupFetch"`
	SizeBytes   int64  `json:"sizeBytes"`
	SizePretty  string `json:"sizePretty"`
}

type SequentialScanStat struct {
	Table       string `json:"table"`
	SeqScan     int64  `json:"seqScan"`
	SeqTupRead  int64  `json:"seqTupRead"`
	IdxScan     int64  `json:"idxScan"`
	IdxTupFetch int64  `json:"idxTupFetch"`
	NLiveTup    int64  `json:"nLiveTup"`
	SizeBytes   int64  `json:"sizeBytes"`
	SizePretty  string `json:"sizePretty"`
}

type BlockedStat struct {
	BlockedPID       int32   `json:"blockedPid"`
	BlockedUser      string  `json:"blockedUser"`
	BlockedForMillis float64 `json:"blockedForMillis"`
	BlockedQuery     string  `json:"blockedQuery"`
	BlockingPID      int32   `json:"blockingPid"`
	BlockingQuery    string  `json:"blockingQuery"`
	WaitEventType    string  `json:"waitEventType"`
	WaitEvent        string  `json:"waitEvent"`
}

type TableStat struct {
	Table         string `json:"table"`
	Rows          int64  `json:"rows"`
	TotalPretty   string `json:"totalPretty"`
	DataPretty    string `json:"dataPretty"`
	IndexesPretty string `json:"indexesPretty"`
	DeadRows      int64  `json:"deadRows"`
	LastVacuum    string `json:"lastVacuum"`
}

type Stats struct {
	Version                       string               `json:"version"`
	DatabaseSizeBytes             int64                `json:"databaseSizeBytes"`
	Connections                   int64                `json:"connections"`
	Active                        int64                `json:"active"`
	Idle                          int64                `json:"idle"`
	IdleInTransaction             int64                `json:"idleInTransaction"`
	CacheHitPercent               float64              `json:"cacheHitPercent"`
	XactCommit                    int64                `json:"xactCommit"`
	XactRollback                  int64                `json:"xactRollback"`
	TupReturned                   int64                `json:"tupReturned"`
	TupFetched                    int64                `json:"tupFetched"`
	TupInserted                   int64                `json:"tupInserted"`
	TupUpdated                    int64                `json:"tupUpdated"`
	TupDeleted                    int64                `json:"tupDeleted"`
	BlksRead                      int64                `json:"blksRead"`
	BlksHit                       int64                `json:"blksHit"`
	BlockSizeBytes                int64                `json:"blockSizeBytes"`
	StatementsCalls               int64                `json:"statementsCalls"`
	StatementsTotalExecTimeMillis float64              `json:"statementsTotalExecTimeMillis"`
	MeanQueryLatencyMillis        float64              `json:"meanQueryLatencyMillis"`
	RowsReadPerSecond             float64              `json:"rowsReadPerSecond"`
	RowsWrittenPerSecond          float64              `json:"rowsWrittenPerSecond"`
	BytesReadPerSecond            float64              `json:"bytesReadPerSecond"`
	BytesHitPerSecond             float64              `json:"bytesHitPerSecond"`
	QueriesPerSecond              float64              `json:"queriesPerSecond"`
	TransactionsPerSecond         float64              `json:"transactionsPerSecond"`
	Sessions                      []SessionStat        `json:"sessions"`
	Statements                    []StatementStat      `json:"statements"`
	Indexes                       []IndexStat          `json:"indexes"`
	SequentialScans               []SequentialScanStat `json:"sequentialScans"`
	Blocked                       []BlockedStat        `json:"blocked"`
	Tables                        []TableStat          `json:"tables"`
}

const statsQueryTruncate = 500

func truncateQuery(query string, limit int) string {
	if limit <= 0 {
		return query
	}
	value := []rune(query)
	if len(value) <= limit {
		return query
	}
	return string(value[:limit])
}

func (client *Client) Stats(ctx context.Context) (Stats, error) {
	return client.collectStats(ctx, true)
}

// CollectorStats returns the counters needed by the managed-stats history
// collector without the heavy diagnostic catalogs used by the live Stats tab.
func (client *Client) CollectorStats(ctx context.Context) (Stats, error) {
	return client.collectStats(ctx, false)
}

func (client *Client) collectStats(ctx context.Context, diagnostics bool) (Stats, error) {
	stats := Stats{
		Sessions:        make([]SessionStat, 0),
		Statements:      make([]StatementStat, 0),
		Indexes:         make([]IndexStat, 0),
		SequentialScans: make([]SequentialScanStat, 0),
		Blocked:         make([]BlockedStat, 0),
		Tables:          make([]TableStat, 0),
	}
	if err := client.connection.QueryRow(ctx, `
SELECT current_setting('server_version'),
       pg_database_size(current_database()),
       (SELECT count(*) FROM pg_stat_activity),
       (SELECT count(*) FROM pg_stat_activity WHERE state = 'active'),
       (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle'),
       (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction'),
       COALESCE(round(100.0 * blks_hit::numeric / nullif(blks_hit + blks_read, 0), 1), 0)::float8,
       xact_commit, xact_rollback,
       tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
       blks_read, blks_hit,
       current_setting('block_size')::bigint
FROM pg_stat_database
WHERE datname = current_database()`).Scan(
		&stats.Version, &stats.DatabaseSizeBytes,
		&stats.Connections, &stats.Active, &stats.Idle, &stats.IdleInTransaction,
		&stats.CacheHitPercent,
		&stats.XactCommit, &stats.XactRollback,
		&stats.TupReturned, &stats.TupFetched, &stats.TupInserted, &stats.TupUpdated, &stats.TupDeleted,
		&stats.BlksRead, &stats.BlksHit, &stats.BlockSizeBytes,
	); err != nil {
		return Stats{}, fmt.Errorf("collect managed PostgreSQL summary stats: %w", err)
	}

	if err := client.collectStatementStats(ctx, &stats); err != nil {
		return Stats{}, err
	}
	if err := client.collectBlockedStats(ctx, &stats, diagnostics); err != nil {
		return Stats{}, err
	}
	if !diagnostics {
		return stats, nil
	}

	sessions, err := client.connection.Query(ctx, `
SELECT pid,
       COALESCE(usename, ''),
       COALESCE(state, ''),
       COALESCE(wait_event, ''),
       EXTRACT(EPOCH FROM (now() - COALESCE(xact_start, query_start, backend_start))) * 1000,
       COALESCE(query, ''),
       COALESCE(client_addr::text, '')
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
ORDER BY COALESCE(xact_start, query_start, backend_start) ASC NULLS LAST
LIMIT 100`)
	if err != nil {
		return Stats{}, fmt.Errorf("collect managed PostgreSQL sessions: %w", err)
	}
	for sessions.Next() {
		var session SessionStat
		var query string
		if err := sessions.Scan(
			&session.PID, &session.Usename, &session.State, &session.WaitEvent,
			&session.DurationMillis, &query, &session.ClientAddr,
		); err != nil {
			sessions.Close()
			return Stats{}, err
		}
		session.Query = truncateQuery(query, statsQueryTruncate)
		stats.Sessions = append(stats.Sessions, session)
	}
	if err := sessions.Err(); err != nil {
		sessions.Close()
		return Stats{}, err
	}
	sessions.Close()

	indexes, err := client.connection.Query(ctx, `
SELECT schemaname, relname, indexrelname,
       idx_scan, idx_tup_read, idx_tup_fetch,
       pg_relation_size(indexrelid),
       pg_size_pretty(pg_relation_size(indexrelid))
FROM pg_stat_user_indexes
ORDER BY idx_scan ASC, pg_relation_size(indexrelid) DESC
LIMIT 50`)
	if err != nil {
		return Stats{}, fmt.Errorf("collect managed PostgreSQL indexes: %w", err)
	}
	for indexes.Next() {
		var index IndexStat
		if err := indexes.Scan(
			&index.Schema, &index.Table, &index.Index,
			&index.IdxScan, &index.IdxTupRead, &index.IdxTupFetch,
			&index.SizeBytes, &index.SizePretty,
		); err != nil {
			indexes.Close()
			return Stats{}, err
		}
		stats.Indexes = append(stats.Indexes, index)
	}
	if err := indexes.Err(); err != nil {
		indexes.Close()
		return Stats{}, err
	}
	indexes.Close()

	scans, err := client.connection.Query(ctx, `
SELECT relname, seq_scan, seq_tup_read, idx_scan, idx_tup_fetch, n_live_tup,
       pg_total_relation_size(relid),
       pg_size_pretty(pg_total_relation_size(relid))
FROM pg_stat_user_tables
ORDER BY seq_tup_read DESC
LIMIT 30`)
	if err != nil {
		return Stats{}, fmt.Errorf("collect managed PostgreSQL sequential scans: %w", err)
	}
	for scans.Next() {
		var scan SequentialScanStat
		if err := scans.Scan(
			&scan.Table, &scan.SeqScan, &scan.SeqTupRead, &scan.IdxScan, &scan.IdxTupFetch, &scan.NLiveTup,
			&scan.SizeBytes, &scan.SizePretty,
		); err != nil {
			scans.Close()
			return Stats{}, err
		}
		stats.SequentialScans = append(stats.SequentialScans, scan)
	}
	if err := scans.Err(); err != nil {
		scans.Close()
		return Stats{}, err
	}
	scans.Close()

	tables, err := client.connection.Query(ctx, `
SELECT relname,
       n_live_tup,
       pg_size_pretty(pg_total_relation_size(relid)),
       pg_size_pretty(pg_relation_size(relid)),
       pg_size_pretty(pg_indexes_size(relid)),
       n_dead_tup,
       COALESCE(last_autovacuum::text, last_vacuum::text, 'never')
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT 100`)
	if err != nil {
		return Stats{}, fmt.Errorf("collect managed PostgreSQL tables: %w", err)
	}
	for tables.Next() {
		var table TableStat
		if err := tables.Scan(
			&table.Table, &table.Rows, &table.TotalPretty, &table.DataPretty,
			&table.IndexesPretty, &table.DeadRows, &table.LastVacuum,
		); err != nil {
			tables.Close()
			return Stats{}, err
		}
		stats.Tables = append(stats.Tables, table)
	}
	if err := tables.Err(); err != nil {
		tables.Close()
		return Stats{}, err
	}
	tables.Close()
	return stats, nil
}

func (client *Client) collectBlockedStats(ctx context.Context, stats *Stats, details bool) error {
	if !details {
		var blockedCount int64
		if err := client.connection.QueryRow(ctx, `
SELECT count(*)
FROM pg_stat_activity AS blocked
WHERE blocked.pid <> pg_backend_pid()
  AND cardinality(pg_blocking_pids(blocked.pid)) > 0`).Scan(&blockedCount); err != nil {
			return fmt.Errorf("collect managed PostgreSQL blocked count: %w", err)
		}
		if blockedCount > 0 {
			stats.Blocked = make([]BlockedStat, blockedCount)
		}
		return nil
	}
	blocked, err := client.connection.Query(ctx, `
SELECT blocked.pid,
       COALESCE(blocked.usename, ''),
       EXTRACT(EPOCH FROM (now() - blocked.query_start)) * 1000,
       COALESCE(blocked.query, ''),
       blocking.pid,
       COALESCE(blocking.query, ''),
       COALESCE(blocked.wait_event_type, ''),
       COALESCE(blocked.wait_event, '')
FROM pg_stat_activity AS blocked
JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS blocking_pid ON true
JOIN pg_stat_activity AS blocking ON blocking.pid = blocking_pid
WHERE blocked.pid <> pg_backend_pid()`)
	if err != nil {
		return fmt.Errorf("collect managed PostgreSQL blocked sessions: %w", err)
	}
	for blocked.Next() {
		var item BlockedStat
		var blockedQuery, blockingQuery string
		if err := blocked.Scan(
			&item.BlockedPID, &item.BlockedUser, &item.BlockedForMillis, &blockedQuery,
			&item.BlockingPID, &blockingQuery, &item.WaitEventType, &item.WaitEvent,
		); err != nil {
			blocked.Close()
			return err
		}
		item.BlockedQuery = truncateQuery(blockedQuery, statsQueryTruncate)
		item.BlockingQuery = truncateQuery(blockingQuery, statsQueryTruncate)
		stats.Blocked = append(stats.Blocked, item)
	}
	if err := blocked.Err(); err != nil {
		blocked.Close()
		return err
	}
	blocked.Close()
	return nil
}

func (client *Client) collectStatementStats(ctx context.Context, stats *Stats) error {
	rows, err := client.connection.Query(ctx, `
SELECT COALESCE(queryid::text, ''),
       COALESCE(query, ''),
       calls,
       total_exec_time,
       mean_exec_time,
       max_exec_time,
       rows,
       shared_blks_hit,
       shared_blks_read,
       temp_blks_read,
       temp_blks_written,
       CASE WHEN sum(total_exec_time) OVER () > 0
         THEN 100.0 * total_exec_time / sum(total_exec_time) OVER ()
         ELSE 0 END,
       coalesce(sum(calls) OVER (), 0),
       coalesce(sum(total_exec_time) OVER (), 0)
FROM pg_stat_statements
ORDER BY (queryid IS NULL), total_exec_time DESC
LIMIT 30`)
	if err != nil {
		// Extension missing or not ready — leave statements empty and counters at zero.
		return nil
	}
	for rows.Next() {
		var statement StatementStat
		var totalCalls int64
		var totalExec float64
		if err := rows.Scan(
			&statement.QueryID, &statement.Query, &statement.Calls,
			&statement.TotalExecTimeMillis, &statement.MeanExecTimeMillis, &statement.MaxExecTimeMillis,
			&statement.Rows, &statement.SharedBlksHit, &statement.SharedBlksRead,
			&statement.TempBlksRead, &statement.TempBlksWritten, &statement.PercentOfTotalTime,
			&totalCalls, &totalExec,
		); err != nil {
			rows.Close()
			return err
		}
		recordStatementStat(stats, statement, totalCalls, totalExec)
	}
	err = rows.Err()
	rows.Close()
	return err
}

// recordStatementStat applies window totals from pg_stat_statements and keeps
// only rows with a stable queryid. Utility statements can report NULL queryid.
func recordStatementStat(stats *Stats, statement StatementStat, totalCalls int64, totalExec float64) {
	stats.StatementsCalls = totalCalls
	stats.StatementsTotalExecTimeMillis = totalExec
	if totalCalls > 0 {
		stats.MeanQueryLatencyMillis = totalExec / float64(totalCalls)
	}
	if statement.QueryID == "" {
		return
	}
	statement.Query = truncateQuery(statement.Query, statsQueryTruncate)
	stats.Statements = append(stats.Statements, statement)
}
