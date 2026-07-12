package state_test

import (
	"context"
	"database/sql"
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
	if version != 1 {
		t.Fatalf("schema version = %d", version)
	}
	var tableCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN ('installation', 'services', 'deployments', 'object_stores', 'managed_postgres', 'managed_redis', 'operations', 'audit_events')").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 8 {
		t.Fatalf("core table count = %d, want 8", tableCount)
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

func TestStartupMarksOnlyNonActiveRunningDeploymentInterrupted(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		statements := []string{
			"INSERT INTO projects(id, name, created_at, updated_at) VALUES ('p', 'project', 1, 1)",
			"INSERT INTO services(id, project_id, name, image_reference, active_deployment_id, created_at, updated_at) VALUES ('s', 'p', 'service', 'example:latest', 'active', 1, 1)",
			"INSERT INTO deployments(id, service_id, image_digest, service_config_hash, snapshot_json, status, created_at) VALUES ('active', 's', 'sha256:a', 'a', '{}', 'running', 1)",
			"INSERT INTO deployments(id, service_id, image_digest, service_config_hash, snapshot_json, status, created_at) VALUES ('candidate', 's', 'sha256:b', 'b', '{}', 'running', 2)",
			"INSERT INTO operations(id, kind, target_id, status, started_at) VALUES ('op', 'cleanup', 's', 'running', 1)",
			"INSERT INTO backups(id, resource_kind, resource_id, status, started_at) VALUES ('backup', 'registry', 'registry', 'running', 1)",
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
