package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateSchemaVersionNineMovesTelemetryToBundledRuntime(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE resource_metric_samples (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE aggregate_metric_samples (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE managed_stat_samples (id TEXT PRIMARY KEY) STRICT;
PRAGMA user_version = 9;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionNine(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 10 {
		t.Fatalf("schema version = %d", version)
	}
	for _, table := range []string{"resource_metric_samples", "aggregate_metric_samples", "managed_stat_samples"} {
		var count int
		if err := database.QueryRow(
			"SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?", table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy table %s remains", table)
		}
	}
	if _, err := database.Exec("INSERT INTO services(id, sentry_public_hostname) VALUES ('one', 'errors.example.com')"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO services(id, sentry_public_hostname) VALUES ('two', 'errors.example.com')"); err == nil {
		t.Fatal("duplicate Sentry hostname was accepted")
	}
}

func TestMigrateSchemaVersionTenAddsServiceTelemetryControlState(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
PRAGMA user_version = 10;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionTen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, tables int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN ('service_telemetry_credentials', 'service_telemetry_webhooks')").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 11 || tables != 2 {
		t.Fatalf("schema version/tables = %d/%d", version, tables)
	}
}

func TestMigrateSchemaVersionTwelveExpandsMetricChartQueries(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
INSERT INTO services(id) VALUES ('service');
CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  metric_name TEXT NOT NULL,
  aggregation TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX service_metric_charts_service_idx ON service_metric_charts(service_id, created_at, id);
INSERT INTO service_metric_charts VALUES ('chart', 'service', 'Queue', 'queue.depth', 'avg', 1, 1);
PRAGMA user_version = 12;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionTwelve(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, limit int
	var operation, filters, visualization string
	if err := database.QueryRow(`
SELECT operation, filters_json, series_limit, visualization FROM service_metric_charts WHERE id = 'chart'`,
	).Scan(&operation, &filters, &limit, &visualization); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 13 || operation != "average" || filters != "[]" || limit != 5 || visualization != "area" {
		t.Fatalf("migrated metric chart = version %d, %s, %s, %d, %s", version, operation, filters, limit, visualization)
	}
}

func TestMigrateSchemaVersionThirteenReplacesMetricBuilderWithSQL(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id), title TEXT NOT NULL,
  metric_name TEXT NOT NULL, operation TEXT NOT NULL, filters_json TEXT NOT NULL,
  group_by TEXT, series_limit INTEGER NOT NULL, visualization TEXT NOT NULL,
  legend TEXT NOT NULL, unit TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
) STRICT;
PRAGMA user_version = 13;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionThirteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, sqlColumn int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT count(*) FROM pragma_table_info('service_metric_charts') WHERE name = 'sql'").Scan(&sqlColumn); err != nil {
		t.Fatal(err)
	}
	if version != 14 || sqlColumn != 1 {
		t.Fatalf("schema version/sql column = %d/%d", version, sqlColumn)
	}
}

func TestMigrateSchemaVersionFourteenScopesMetricCharts(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
PRAGMA foreign_keys = ON;
CREATE TABLE projects (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE services (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE) STRICT;
INSERT INTO projects(id) VALUES ('project');
INSERT INTO services(id, project_id) VALUES ('service', 'project');
CREATE TABLE service_metric_charts (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  title TEXT NOT NULL, sql TEXT NOT NULL, visualization TEXT NOT NULL,
  legend TEXT NOT NULL, unit TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
) STRICT;
INSERT INTO service_metric_charts VALUES (
  'chart', 'service', 'Queue', 'SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket',
  'area', 'Depth', '{job}', 1, 1
);
PRAGMA user_version = 14;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionFourteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version int
	var scopeKind, serviceID, title string
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT scope_kind, service_id, title FROM metric_charts WHERE id = 'chart'`).
		Scan(&scopeKind, &serviceID, &title); err != nil {
		t.Fatal(err)
	}
	if version != 15 || scopeKind != MetricScopeService || serviceID != "service" || title != "Queue" {
		t.Fatalf("migrated scoped chart = version %d, %s, %s, %s", version, scopeKind, serviceID, title)
	}
}

func TestMigrateSchemaVersionFifteenAddsBrowserTunnelPath(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
INSERT INTO services(id) VALUES ('service');
PRAGMA user_version = 15;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionFifteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version int
	var columnCount int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT count(*) FROM pragma_table_info('services') WHERE name = 'sentry_tunnel_path'").Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if version != 16 || columnCount != 1 {
		t.Fatalf("schema version/tunnel column = %d/%d", version, columnCount)
	}
}

func TestMigrateSchemaVersionSeventeenAddsMailTables(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE projects (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
PRAGMA user_version = 17;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionSeventeen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, tables int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
SELECT count(*) FROM sqlite_schema
WHERE type = 'table' AND name IN ('smtp_settings', 'mail_error_alerts', 'mail_metric_alerts')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 18 || tables != 3 {
		t.Fatalf("schema version/tables = %d/%d", version, tables)
	}
}

func TestMigrateSchemaVersionEighteenAddsHostCatalog(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
PRAGMA user_version = 18;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionEighteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, tables, hostColumn int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
SELECT count(*) FROM sqlite_schema
WHERE type = 'table' AND name IN ('hosts', 'host_join_tokens')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('services') WHERE name = 'host_id'`).Scan(&hostColumn); err != nil {
		t.Fatal(err)
	}
	if version != 19 || tables != 2 || hostColumn != 1 {
		t.Fatalf("schema version/tables/host_id = %d/%d/%d", version, tables, hostColumn)
	}
}

func TestMigrateSchemaVersionNineteenScopesListenersPerService(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE services (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE service_listeners (
  protocol TEXT NOT NULL,
  public_port INTEGER NOT NULL,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  target_port INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (protocol, public_port)
) WITHOUT ROWID, STRICT;
INSERT INTO services(id) VALUES ('api');
INSERT INTO service_listeners(protocol, public_port, service_id, target_port, created_at)
VALUES ('tcp', 2222, 'api', 22, 1);
PRAGMA user_version = 19;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionNineteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, pk int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
SELECT count(*) FROM pragma_table_info('service_listeners')
WHERE name IN ('service_id', 'protocol', 'public_port') AND pk > 0`).Scan(&pk); err != nil {
		t.Fatal(err)
	}
	if version != 20 || pk != 3 {
		t.Fatalf("schema version/pk columns = %d/%d", version, pk)
	}
}
