package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type manualRunnerNoop struct{}

func (manualRunnerNoop) TryRunNow(context.Context, string, string, int) (state.BackupRecord, error) {
	return state.BackupRecord{}, nil
}

func TestResourceApplicationListsVerifiedRemoteGenerationsWithoutSQLiteCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "demo", AuditEventID: "project-audit",
		ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "redis-1", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "volume", PasswordEncrypted: []byte("sealed"), AuditEventID: "redis-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "user@example.com", CreatedAtMillis: 11,
	}); err != nil {
		t.Fatal(err)
	}
	remote := newMemoryControlRemote()
	for index, generationID := range []string{"generation-1", "generation-2"} {
		built := resourcePublicationBuild(
			t, master, "redis", "redis-1", generationID, []byte(generationID), time.Unix(int64(20+index), 0),
		)
		if err := PublishResource(ctx, remote, master, built); err != nil {
			os.RemoveAll(built.WorkDirectory)
			t.Fatal(err)
		}
		os.RemoveAll(built.WorkDirectory)
	}
	application, err := NewResourceApplication(ResourceApplicationConfig{
		Store: store, Worker: manualRunnerNoop{}, Target: target, TargetGate: targetGate, Master: master,
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return remote, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	generations, err := application.Generations(ctx, "redis", "redis-1")
	if err != nil || len(generations) != 2 || generations[0].GenerationID != "generation-2" {
		t.Fatalf("remote generations = %+v, %v", generations, err)
	}
	var rows int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM backups WHERE resource_kind = 'redis'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("remote generation list was cached in SQLite; rows = %d", rows)
	}
	entries, err := os.ReadDir(filepath.Join(root, "work"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("generation list created local work files: %v", entries)
	}
}
