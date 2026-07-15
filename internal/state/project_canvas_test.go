package state

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectCanvasReturnsExplicitResourceConnections(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, image_reference, environment_json, created_at, updated_at)
VALUES
  ('api', 'project', 'api', 'example/api:latest', '{"DATABASE_URL":"postgres://owner@db.shop.internal:5432/app","CACHE_HOST":"cache"}', 1, 1),
  ('worker', 'project', 'worker', 'example/worker:latest', '{"API_URL":"http://api:8080","UNRELATED":"postgres://remote.example/app"}', 1, 1),
  ('postgres-service', 'project', 'postgres', 'example/postgres-proxy:latest', '{}', 1, 1);
INSERT INTO deployments(id, service_id, image_digest, service_config_hash, snapshot_json, status, error_code, error_message, created_at, finished_at)
VALUES ('failed-deployment', 'worker', 'sha256:worker', 'config', '{}', 'failed', 'readiness_failed', 'worker health check failed', 2, 3);
INSERT INTO managed_postgres(id, project_id, name, image_tag, image_digest, volume_id, database_name, owner_username, owner_password_encrypted, bootstrap_password_encrypted, created_at, updated_at)
VALUES ('db', 'project', 'db', '17', 'sha256:db', 'db-volume', 'app', 'owner', x'01', x'02', 1, 1);
INSERT INTO managed_redis(id, project_id, name, image_tag, image_digest, volume_id, password_encrypted, created_at, updated_at)
VALUES ('cache', 'project', 'cache', '8', 'sha256:cache', 'cache-volume', x'01', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`
INSERT INTO service_resource_variable_refs(service_id, environment_name, resource_kind, resource_id, output_name)
VALUES
  ('api', 'CACHE_HOST', 'redis', 'cache', 'REDISHOST'),
  ('api', 'DATABASE_URL', 'postgres', 'db', 'DATABASE_URL'),
  ('worker', 'API_URL', 'service', 'api', 'URL')`); err != nil {
		t.Fatal(err)
	}

	canvas, err := store.ProjectCanvas(context.Background(), "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(canvas.Resources) != 5 {
		t.Fatalf("resources = %d, want 5", len(canvas.Resources))
	}
	for _, resource := range canvas.Resources {
		if resource.ID == "worker" && (resource.Status != "failed" || resource.StatusMessage != "worker health check failed") {
			t.Fatalf("worker status/message = %q/%q", resource.Status, resource.StatusMessage)
		}
	}
	want := []CanvasConnection{
		{SourceID: "api", TargetID: "cache", EnvironmentNames: []string{"CACHE_HOST"}},
		{SourceID: "api", TargetID: "db", EnvironmentNames: []string{"DATABASE_URL"}},
		{SourceID: "worker", TargetID: "api", EnvironmentNames: []string{"API_URL"}},
	}
	if !reflect.DeepEqual(canvas.Connections, want) {
		t.Fatalf("connections = %#v, want %#v", canvas.Connections, want)
	}
}

func TestProjectCanvasReturnsNotFound(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ProjectCanvas(context.Background(), "missing"); err != ErrProjectNotFound {
		t.Fatalf("error = %v, want ErrProjectNotFound", err)
	}
}
