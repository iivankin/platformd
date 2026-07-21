package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteProjectRemovesOnlyOwnedState(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('delete-me', 'retired', 1, 1), ('keep-me', 'production', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, created_at, updated_at) VALUES
 ('service-delete', 'delete-me', 'api', '{"type":"public_image","image":{"reference":"example/api:latest"}}', '{}', 1, 1),
 ('service-keep', 'keep-me', 'api', '{"type":"public_image","image":{"reference":"example/api:latest"}}', '{}', 1, 1);
INSERT INTO volumes(id, project_id, service_id, name, owner_uid, owner_gid, created_at, updated_at)
 VALUES ('volume-delete', 'delete-me', 'service-delete', 'data', 1000, 1000, 1, 1);
INSERT INTO service_volume_mounts(service_id, volume_id, container_path) VALUES ('service-delete', 'volume-delete', '/data');
INSERT INTO secrets(id, project_id, name, value_encrypted, created_at) VALUES ('secret-delete', 'delete-me', 'TOKEN', x'01', 1);
INSERT INTO service_secret_refs(service_id, environment_name, secret_id) VALUES ('service-delete', 'TOKEN', 'secret-delete');
INSERT INTO managed_postgres(id, project_id, name, image_tag, image_digest, volume_id, database_name, owner_username, owner_password_encrypted, bootstrap_password_encrypted, created_at, updated_at)
 VALUES ('postgres-delete', 'delete-me', 'db', '18', 'sha256:db', 'postgres-volume', 'app', 'app', x'01', x'02', 1, 1);
INSERT INTO managed_redis(id, project_id, name, image_tag, image_digest, volume_id, password_encrypted, created_at, updated_at)
 VALUES ('redis-delete', 'delete-me', 'cache', '8', 'sha256:redis', 'redis-volume', x'01', 1, 1);
INSERT INTO object_stores(id, project_id, name, bucket_name, created_at, updated_at)
 VALUES ('store-delete', 'delete-me', 'assets', 'assets', 1, 1);
INSERT INTO object_payloads(id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at)
 VALUES ('payload-delete', 'store-delete', 1, 1, '00', 1);
INSERT INTO objects(object_store_id, object_key, payload_id, etag, size, created_at, updated_at)
 VALUES ('store-delete', 'file', 'payload-delete', 'etag', 1, 1, 1);
INSERT INTO network_gateways(id, project_id, name, mode, transport, protocol, interface_name, source_address, listen_port, target_service_id, target_port, created_at, updated_at)
 VALUES ('gateway-delete', 'delete-me', 'export', 'export', 'vpc', 'tcp', 'wg0', '10.0.0.1', 15432, 'service-delete', 8080, 1, 1);
INSERT INTO runtime_deployments(id, resource_kind, resource_id, image_tag, image_digest, status, created_at)
 VALUES ('runtime-delete', 'postgres', 'postgres-delete', '18', 'sha256:db', 'succeeded', 1);
INSERT INTO resource_metric_samples(resource_kind, resource_id, observed_at, cpu_usage_micros, memory_bytes, running)
 VALUES ('service', 'service-delete', 1, 1, 1, 1), ('service', 'service-keep', 1, 1, 1, 1);
INSERT INTO backups(id, target_id, resource_kind, resource_id, status, started_at)
 VALUES ('backup-delete', 'removed-target', 'volume', 'volume-delete', 'succeeded', 1);
INSERT INTO operations(id, kind, target_id, status, started_at)
 VALUES ('operation-delete', 'restore', 'postgres-delete', 'succeeded', 1);`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.DeleteProject(context.Background(), DeleteProjectInput{
		ID: "delete-me", ExpectedName: "retired", DeleteBackups: true,
		AuditEventID: "audit-delete", ActorKind: "access", ActorID: "actor", ActorEmail: "actor@example.com",
		RequestCorrelationID: "request-delete", DeletedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Services) != 1 || len(plan.Postgres) != 1 || len(plan.Redis) != 1 || len(plan.ObjectStores) != 1 || len(plan.Gateways) != 1 || len(plan.Volumes) != 1 {
		t.Fatalf("incomplete deletion plan: %+v", plan)
	}
	if _, err := store.Project(context.Background(), "delete-me"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("deleted project lookup error = %v", err)
	}
	if _, err := store.Project(context.Background(), "keep-me"); err != nil {
		t.Fatalf("unrelated project was changed: %v", err)
	}
	for _, table := range []string{"services", "volumes", "managed_postgres", "managed_redis", "object_stores", "object_payloads", "network_gateways", "runtime_deployments", "backups", "operations"} {
		var count int
		if err := store.database.QueryRow("SELECT count(*) FROM " + table + " WHERE id LIKE '%-delete'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d project rows", table, count)
		}
	}
	var keptMetrics, audit int
	if err := store.database.QueryRow("SELECT count(*) FROM resource_metric_samples WHERE resource_id = 'service-keep'").Scan(&keptMetrics); err != nil {
		t.Fatal(err)
	}
	if err := store.database.QueryRow("SELECT count(*) FROM audit_events WHERE id = 'audit-delete' AND action = 'project.delete'").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if keptMetrics != 1 || audit != 1 {
		t.Fatalf("kept metrics/audit = %d/%d", keptMetrics, audit)
	}
}

func TestDeleteProjectRequiresExactName(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec("INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'production', 1, 1)"); err != nil {
		t.Fatal(err)
	}
	_, err = store.DeleteProject(context.Background(), DeleteProjectInput{
		ID: "project", ExpectedName: "wrong", AuditEventID: "audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "actor@example.com", DeletedAtMillis: 2,
	})
	if !errors.Is(err, ErrProjectChanged) {
		t.Fatalf("error = %v, want ErrProjectChanged", err)
	}
}
