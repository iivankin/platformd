package state_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestOpenCreatesHardenedCurrentSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "platformd.db")
	store, err := state.Open(context.Background(), path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %04o", got)
	}
	var version int
	if err := store.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 7 || state.SupportedSchemaVersion() != 7 {
		t.Fatalf("schema version = %d", version)
	}
	var tableCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN ('installation', 'services', 'deployments', 'runtime_deployments', 'object_stores', 'managed_postgres', 'managed_redis', 'service_image_revisions', 'service_image_uploads', 'preview_deployments', 'operations', 'audit_events')").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 12 {
		t.Fatalf("core table count = %d, want 12", tableCount)
	}
}

func TestOpenMigratesSchemaVersionOneWithoutLosingMetricRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "platformd.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
CREATE TABLE resource_metric_samples (marker TEXT NOT NULL) STRICT;
CREATE TABLE aggregate_metric_samples (marker TEXT NOT NULL) STRICT;
CREATE TABLE backup_targets (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE installation (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  id TEXT NOT NULL UNIQUE,
  admin_hostname TEXT NOT NULL UNIQUE,
  registry_hostname TEXT UNIQUE,
  access_team_domain TEXT NOT NULL,
  access_audience TEXT NOT NULL,
  console_passphrase_phc TEXT NOT NULL,
  recovery_mode INTEGER NOT NULL DEFAULT 0 CHECK (recovery_mode IN (0, 1)),
  backup_control_target_id TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE services (id TEXT PRIMARY KEY, active_deployment_id TEXT, enabled INTEGER NOT NULL, source_json TEXT NOT NULL, build_environment_json TEXT NOT NULL) STRICT;
CREATE TABLE deployments (id TEXT PRIMARY KEY, service_id TEXT NOT NULL) STRICT;
CREATE TABLE preview_deployments (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE registry_uploads (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE registry_tags (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE registry_manifests (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE registry_credentials (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE registry_repositories (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE github_app_settings (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE backups (
  id TEXT PRIMARY KEY, target_id TEXT NOT NULL, resource_kind TEXT NOT NULL,
  resource_id TEXT NOT NULL, scheduled_occurrence INTEGER, generation_id TEXT,
  status TEXT NOT NULL, size_bytes INTEGER, error_code TEXT, error_message TEXT,
  started_at INTEGER NOT NULL, finished_at INTEGER
) STRICT;
CREATE INDEX backups_resource_started_idx ON backups(target_id, resource_kind, resource_id, started_at DESC);
CREATE UNIQUE INDEX backups_scheduled_occurrence_idx ON backups(resource_kind, resource_id, scheduled_occurrence) WHERE scheduled_occurrence IS NOT NULL;
INSERT INTO resource_metric_samples(marker) VALUES ('resource');
INSERT INTO aggregate_metric_samples(marker) VALUES ('aggregate');
INSERT INTO installation(
  singleton, id, admin_hostname, registry_hostname, access_team_domain, access_audience,
  console_passphrase_phc, recovery_mode, created_at, updated_at
) VALUES (
  1, 'install', 'admin.example.com', 'registry.example.com', 'example.cloudflareaccess.com',
  'audience', 'phc', 0, 1, 1
);
INSERT INTO services(id, active_deployment_id, enabled, source_json, build_environment_json)
VALUES
  ('github-service', 'github-deployment', 1, '{"type":"github"}', '{"TOKEN":"secret"}'),
  ('registry-service', 'registry-deployment', 1, '{"type":"platformd_registry"}', '{}'),
  ('public-service', 'public-deployment', 1, '{"type":"public_image"}', '{}');
INSERT INTO deployments(id, service_id)
VALUES
  ('github-deployment', 'github-service'),
  ('registry-deployment', 'registry-service'),
  ('public-deployment', 'public-service');
PRAGMA user_version = 1;`); err != nil {
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
	if version != state.SupportedSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, state.SupportedSchemaVersion())
	}
	for _, table := range []string{"resource_metric_samples", "aggregate_metric_samples"} {
		var diskColumns int
		if err := store.QueryRowContext(context.Background(),
			"SELECT count(*) FROM pragma_table_info(?) WHERE name = 'disk_bytes'", table,
		).Scan(&diskColumns); err != nil {
			t.Fatal(err)
		}
		if diskColumns != 1 {
			t.Fatalf("%s disk column count = %d, want 1", table, diskColumns)
		}
		var rows int
		if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("%s row count = %d, want 1", table, rows)
		}
	}
	for _, serviceID := range []string{"github-service", "registry-service"} {
		var sourceJSON string
		var enabled int
		var activeDeployment sql.NullString
		if err := store.QueryRowContext(context.Background(),
			"SELECT source_json, enabled, active_deployment_id FROM services WHERE id = ?", serviceID,
		).Scan(&sourceJSON, &enabled, &activeDeployment); err != nil {
			t.Fatal(err)
		}
		if sourceJSON != `{"type":"unconfigured"}` || enabled != 0 || activeDeployment.Valid {
			t.Fatalf("migrated service %s = source %s, enabled %d, active deployment %v", serviceID, sourceJSON, enabled, activeDeployment)
		}
	}
	var publicDeployment string
	if err := store.QueryRowContext(context.Background(), `
SELECT deployments.id FROM services
JOIN deployments ON deployments.id = services.active_deployment_id
WHERE services.id = 'public-service' AND services.enabled = 1 AND json_extract(services.source_json, '$.type') = 'public_image'`,
	).Scan(&publicDeployment); err != nil {
		t.Fatal(err)
	}
	if publicDeployment != "public-deployment" {
		t.Fatalf("preserved public deployment = %s", publicDeployment)
	}
	var removedDeployments int
	if err := store.QueryRowContext(context.Background(), `
SELECT count(*) FROM deployments WHERE id IN ('github-deployment', 'registry-deployment')`,
	).Scan(&removedDeployments); err != nil {
		t.Fatal(err)
	}
	if removedDeployments != 0 {
		t.Fatalf("obsolete deployment count = %d, want 0", removedDeployments)
	}
	var registryHostnameColumns int
	if err := store.QueryRowContext(context.Background(),
		"SELECT count(*) FROM pragma_table_info('installation') WHERE name = 'registry_hostname'",
	).Scan(&registryHostnameColumns); err != nil {
		t.Fatal(err)
	}
	if registryHostnameColumns != 0 {
		t.Fatalf("registry_hostname column count = %d, want 0", registryHostnameColumns)
	}
	var installationID string
	if err := store.QueryRowContext(context.Background(), "SELECT id FROM installation WHERE singleton = 1").Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	if installationID != "install" {
		t.Fatalf("preserved installation id = %s", installationID)
	}
}

func TestOpenReopensCurrentSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platformd.db")
	store, err := state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'project', 1, 1)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id = 'project'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("preserved project count = %d, want 1", count)
	}
}

func TestWriterSerializesTransactions(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	defer store.Close()

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{}, 1)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		if err := store.Write(context.Background(), func(transaction *sql.Tx) error {
			close(firstEntered)
			<-releaseFirst
			_, err := transaction.Exec("INSERT INTO projects(id, name, created_at, updated_at) VALUES ('first', 'first', 1, 1)")
			return err
		}); err != nil {
			t.Errorf("first write: %v", err)
		}
	}()
	<-firstEntered
	go func() {
		defer group.Done()
		if err := store.Write(context.Background(), func(transaction *sql.Tx) error {
			secondEntered <- struct{}{}
			_, err := transaction.Exec("INSERT INTO projects(id, name, created_at, updated_at) VALUES ('second', 'second', 2, 2)")
			return err
		}); err != nil {
			t.Errorf("second write: %v", err)
		}
	}()

	select {
	case <-secondEntered:
		t.Fatal("second transaction entered before first was released")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	group.Wait()

	var count int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM projects").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("project count = %d", count)
	}
}

func TestControlObserverRunsOnlyAfterSuccessfulControlCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	commits := 0
	store.SetControlCommitObserver(func() { commits++ })
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "INSERT INTO projects(id, name, created_at, updated_at) VALUES ('runtime', 'runtime', 1, 1)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if commits != 0 {
		t.Fatalf("ordinary write notified control observer %d times", commits)
	}
	if err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "INSERT INTO projects(id, name, created_at, updated_at) VALUES ('control', 'control', 2, 2)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatalf("control commit notified observer %d times", commits)
	}
	if err := store.WriteControl(ctx, func(*sql.Tx) error { return errors.New("rollback") }); err == nil {
		t.Fatal("failed control transaction succeeded")
	}
	if commits != 1 {
		t.Fatalf("failed control transaction notified observer %d times", commits)
	}
}

func TestStartupMarksOnlyNonActiveRunningDeploymentInterrupted(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		statements := []string{
			"INSERT INTO projects(id, name, created_at, updated_at) VALUES ('p', 'project', 1, 1)",
			"INSERT INTO services(id, project_id, name, source_json, active_deployment_id, created_at, updated_at) VALUES ('s', 'p', 'service', '{\"type\":\"public_image\",\"image\":{\"reference\":\"example:latest\"}}', 'active', 1, 1)",
			"INSERT INTO deployments(id, service_id, image_digest, image_reference, service_config_hash, snapshot_json, status, created_at) VALUES ('active', 's', 'sha256:a', 'example:latest', 'a', '{}', 'running', 1)",
			"INSERT INTO deployments(id, service_id, image_digest, image_reference, service_config_hash, snapshot_json, status, created_at) VALUES ('candidate', 's', 'sha256:b', 'example:latest', 'b', '{}', 'running', 2)",
			"INSERT INTO operations(id, kind, target_id, status, started_at) VALUES ('op', 'cleanup', 's', 'running', 1)",
			"INSERT INTO backups(id, target_id, resource_kind, resource_id, generation_id, status, started_at) VALUES ('backup', 'target', 'image', 'service', 'generation', 'running', 1)",
		}
		for _, statement := range statements {
			if _, err := transaction.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInterrupted(ctx, 99); err != nil {
		t.Fatal(err)
	}

	for id, expected := range map[string]string{"active": "running", "candidate": "interrupted"} {
		var status string
		if err := store.QueryRowContext(ctx, "SELECT status FROM deployments WHERE id = ?", id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != expected {
			t.Fatalf("deployment %s status = %s, want %s", id, status, expected)
		}
	}
	for table := range map[string]struct{}{"operations": {}, "backups": {}} {
		var status string
		if err := store.QueryRowContext(ctx, "SELECT status FROM "+table+" LIMIT 1").Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "interrupted" {
			t.Fatalf("%s status = %s", table, status)
		}
	}
}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
