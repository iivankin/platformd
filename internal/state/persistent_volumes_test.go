package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentVolumeReferencesIncludesEveryAuthoritativePointer(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	seedPersistentVolumeRows(t, store)

	references, err := store.PersistentVolumeReferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 {
		t.Fatalf("references = %+v", references)
	}
	want := []PersistentVolumeReference{
		{ProjectID: "project", VolumeID: "ordinary-volume", Kind: PersistentVolumeOrdinary, OwnerUID: 123, OwnerGID: 456},
		{ProjectID: "project", VolumeID: "postgres-volume", Kind: PersistentVolumePostgres},
		{ProjectID: "project", VolumeID: "redis-volume", Kind: PersistentVolumeRedis},
	}
	for index := range want {
		if references[index] != want[index] {
			t.Fatalf("reference[%d] = %+v, want %+v", index, references[index], want[index])
		}
	}
}

func TestPersistentVolumeReferencesRejectsSharedFilesystemPath(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	seedPersistentVolumeRows(t, store)
	if _, err := store.database.ExecContext(context.Background(), `
UPDATE managed_redis SET volume_id = 'ordinary-volume' WHERE id = 'redis'`); err != nil {
		t.Fatal(err)
	}

	_, err := store.PersistentVolumeReferences(context.Background())
	if err == nil || !strings.Contains(err.Error(), "shared by ordinary and redis") {
		t.Fatalf("shared path error = %v", err)
	}
}

func openPersistentVolumeStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func seedPersistentVolumeRows(t *testing.T, store *Store) {
	t.Helper()
	_, err := store.database.ExecContext(context.Background(), `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(
  id, project_id, name, image_reference, environment_json,
  health_timeout_seconds, enabled, created_at, updated_at
) VALUES ('service', 'project', 'web', 'example/image:latest', '{}', 60, 1, 1, 1);
INSERT INTO volumes(id, project_id, service_id, name, owner_uid, owner_gid, created_at, updated_at)
VALUES ('ordinary-volume', 'project', 'service', 'data', 123, 456, 1, 1);
INSERT INTO managed_postgres(
  id, project_id, name, image_tag, image_digest, volume_id, database_name,
  owner_username, owner_password_encrypted, bootstrap_password_encrypted,
  created_at, updated_at
) VALUES (
  'postgres', 'project', 'db', '18',
  'sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4',
  'postgres-volume', 'app', 'owner', x'01', x'02', 1, 1
);
INSERT INTO managed_redis(
  id, project_id, name, image_tag, image_digest, volume_id,
  password_encrypted, created_at, updated_at
) VALUES (
  'redis', 'project', 'cache', '8',
  'sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4',
  'redis-volume', x'03', 1, 1
)`)
	if err != nil {
		t.Fatal(err)
	}
}
