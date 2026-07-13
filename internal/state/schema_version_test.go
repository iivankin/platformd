package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestReadSchemaVersionIsReadOnlyAndDoesNotCreateMissingState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "platformd.db")
	if _, err := state.ReadSchemaVersion(ctx, path, os.Geteuid()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing schema read = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only schema check created state: %v", err)
	}
	store, err := state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	version, err := state.ReadSchemaVersion(ctx, path, os.Geteuid())
	if err != nil || version != state.SupportedSchemaVersion() {
		t.Fatalf("schema version = %d, %v", version, err)
	}
}

func TestInspectDatabaseChecksIntegrityWithoutMigrating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := state.InspectDatabase(ctx, path, os.Geteuid(), true)
	if err != nil || inspection.SchemaVersion != state.SupportedSchemaVersion() {
		t.Fatalf("database inspection = %+v, %v", inspection, err)
	}
}
