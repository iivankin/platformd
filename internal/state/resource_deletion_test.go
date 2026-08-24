package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteManagedResourcesRetainsBackupsAndRecordsAudits(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO managed_postgres(
  id, project_id, name, image_tag, image_digest, volume_id, database_name,
  owner_username, owner_password_encrypted, bootstrap_password_encrypted,
  created_at, updated_at
) VALUES ('postgres', 'project', 'database', '17', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  'postgres-volume', 'app', 'owner', X'01', X'02', 2, 2);
INSERT INTO managed_postgres_extensions(postgres_id, name, version, recipe_digest, created_at, updated_at)
VALUES ('postgres', 'vector', '1', 'recipe', 2, 2);
INSERT INTO managed_redis(
  id, project_id, name, image_tag, image_digest, volume_id, password_encrypted,
  created_at, updated_at
) VALUES ('redis', 'project', 'cache', '8', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  'redis-volume', X'03', 3, 3);
INSERT INTO object_stores(id, project_id, name, bucket_name, created_at, updated_at)
VALUES ('objects', 'project', 'assets', 'assets', 4, 4);
INSERT INTO s3_credentials(id, object_store_id, name, permission, secret_encrypted, created_at)
VALUES ('credential', 'objects', 'default', 'read_write', X'04', 4);
INSERT INTO runtime_deployments(
  id, resource_kind, resource_id, image_tag, image_digest, status, active, created_at
) VALUES
  ('postgres-runtime', 'postgres', 'postgres', '17', 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'succeeded', 1, 5),
  ('redis-runtime', 'redis', 'redis', '8', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'succeeded', 1, 5);
INSERT INTO operations(id, kind, target_id, status, started_at) VALUES
  ('postgres-operation', 'postgres_restore', 'postgres', 'succeeded', 5),
  ('redis-operation', 'redis_restore', 'redis', 'succeeded', 5),
  ('objects-operation', 'object_store_restore', 'objects', 'succeeded', 5);
INSERT INTO backups(id, target_id, resource_kind, resource_id, generation_id, status, size_bytes, started_at, finished_at) VALUES
  ('postgres-backup', 'target', 'postgres', 'postgres', 'generation-postgres', 'succeeded', 10, 5, 6),
  ('redis-backup', 'target', 'redis', 'redis', 'generation-redis', 'succeeded', 10, 5, 6),
  ('objects-backup', 'target', 'object_store', 'objects', 'generation-objects', 'succeeded', 10, 5, 6)`); err != nil {
		t.Fatal(err)
	}

	deletion := func(id string, expected int64, auditID string) DeleteResourceInput {
		return DeleteResourceInput{
			ID: id, ProjectID: "project", ExpectedUpdatedMillis: expected,
			AuditEventID: auditID, ActorKind: "access", ActorID: "actor",
			ActorEmail: "admin@example.com", RequestCorrelationID: auditID + "-request",
			DeletedAtMillis: 10,
		}
	}
	if _, err := store.DeleteManagedPostgres(context.Background(), deletion("postgres", 2, "postgres-delete")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteManagedRedis(context.Background(), deletion("redis", 3, "redis-delete")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteObjectStore(context.Background(), deletion("objects", 4, "objects-delete")); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"managed_postgres", "managed_postgres_extensions", "managed_redis", "object_stores",
		"s3_credentials", "runtime_deployments", "operations",
	} {
		var count int
		if err := store.database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
	var backups int
	if err := store.database.QueryRow("SELECT count(*) FROM backups").Scan(&backups); err != nil || backups != 3 {
		t.Fatalf("retained backup count = %d, %v", backups, err)
	}
	var audits int
	if err := store.database.QueryRow(`
SELECT count(*) FROM audit_events
WHERE action IN ('postgres.delete', 'redis.delete', 'object_store.delete')
  AND json_extract(metadata_json, '$.backupsRetained') = 1`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("resource deletion audit count = %d, %v", audits, err)
	}
}

func TestDeleteManagedResourceRejectsStaleVersionWithoutPartialCleanup(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO managed_redis(
  id, project_id, name, image_tag, image_digest, volume_id, password_encrypted,
  created_at, updated_at
) VALUES ('redis', 'project', 'cache', '8', 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  'redis-volume', X'03', 3, 3);
INSERT INTO runtime_deployments(
  id, resource_kind, resource_id, image_tag, image_digest, status, active, created_at
) VALUES ('redis-runtime', 'redis', 'redis', '8',
  'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'succeeded', 1, 5);
INSERT INTO operations(id, kind, target_id, status, started_at)
VALUES ('redis-operation', 'redis_restore', 'redis', 'succeeded', 5)`); err != nil {
		t.Fatal(err)
	}

	_, err = store.DeleteManagedRedis(context.Background(), DeleteResourceInput{
		ID: "redis", ProjectID: "project", ExpectedUpdatedMillis: 2,
		AuditEventID: "redis-delete", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", DeletedAtMillis: 10,
	})
	if !errors.Is(err, ErrManagedRedisChanged) {
		t.Fatalf("stale deletion error = %v", err)
	}
	for _, table := range []string{"managed_redis", "runtime_deployments", "operations"} {
		var count int
		if err := store.database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count after stale deletion = %d, %v", table, count, err)
		}
	}
}
