package state_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestOpenMigratesSchemaVersionEightToManagedStatSamples(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "platformd.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
CREATE TABLE resource_metric_samples (
  resource_kind TEXT NOT NULL CHECK (resource_kind IN ('service', 'postgres', 'redis')),
  resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  duration_millis INTEGER NOT NULL CHECK (duration_millis > 0),
  cpu_duration_millis INTEGER NOT NULL DEFAULT 0,
  network_duration_millis INTEGER NOT NULL DEFAULT 0,
  proxy_duration_millis INTEGER NOT NULL DEFAULT 0,
  cpu_millicores INTEGER,
  cpu_peak_millicores INTEGER,
  memory_bytes INTEGER NOT NULL,
  memory_peak_bytes INTEGER NOT NULL,
  disk_bytes INTEGER,
  network_ingress_bytes_per_second INTEGER,
  network_ingress_peak_bytes_per_second INTEGER,
  network_egress_bytes_per_second INTEGER,
  network_egress_peak_bytes_per_second INTEGER,
  running INTEGER NOT NULL,
  proxy_metrics_json TEXT,
  PRIMARY KEY (resource_kind, resource_id, observed_at)
) WITHOUT ROWID, STRICT;
INSERT INTO resource_metric_samples(
  resource_kind, resource_id, observed_at, duration_millis,
  memory_bytes, memory_peak_bytes, running
) VALUES ('service', 'api', 10, 1000, 1, 1, 1);
PRAGMA user_version = 8;`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := state.Open(context.Background(), path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var version int
	if err := store.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 9 {
		t.Fatalf("schema version = %d, want 9", version)
	}
	var managedTable, keptRows int
	if err := store.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'managed_stat_samples'`,
	).Scan(&managedTable); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRowContext(context.Background(),
		`SELECT count(*) FROM resource_metric_samples WHERE resource_id = 'api'`,
	).Scan(&keptRows); err != nil {
		t.Fatal(err)
	}
	if managedTable != 1 || keptRows != 1 {
		t.Fatalf("managed table / kept rows = %d / %d", managedTable, keptRows)
	}
	if err := store.RecordManagedStatBatch(context.Background(), []state.ManagedStatSample{{
		Kind: "postgres", ResourceID: "db", ObservedAt: 20, MetricsJSON: `{"connections":1}`,
	}}, 1); err != nil {
		t.Fatal(err)
	}
	samples, err := store.ManagedStatSamples(context.Background(), "postgres", "db", 1, 100)
	if err != nil || len(samples) != 1 || samples[0].MetricsJSON != `{"connections":1}` {
		t.Fatalf("managed samples = %+v, %v", samples, err)
	}
}
