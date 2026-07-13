package state_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestOnlineBackupProducesConsistentStandaloneSQLiteImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openStore(t)
	for index := 0; index < 200; index++ {
		identifier := fmt.Sprintf("project-%03d", index)
		_, err := store.CreateProject(ctx, state.CreateProject{
			ID: identifier, Name: identifier, AuditEventID: "audit-" + identifier,
			ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: int64(index + 1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "snapshot", "platformd.db")
	if err := store.OnlineBackup(ctx, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup file = %+v, %v", info, err)
	}
	backup, err := state.Open(ctx, destination, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	projects, err := backup.Projects(ctx)
	if err != nil || len(projects) != 200 {
		t.Fatalf("backup projects = %d, %v", len(projects), err)
	}
	var integrity string
	if err := backup.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity = %q, %v", integrity, err)
	}
}
