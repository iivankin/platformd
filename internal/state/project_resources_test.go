package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectAndResourceByName(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project-id', 'shop', 1, 1);
INSERT INTO services(id, project_id, name, source_json, environment_json, created_at, updated_at)
VALUES ('service-id', 'project-id', 'api', '{"type":"public_image","image":{"reference":"example/api:latest"}}', '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}

	project, err := store.ProjectByName(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if project.ID != "project-id" || project.Name != "shop" || project.ServiceCount != 1 {
		t.Fatalf("project = %+v", project)
	}
	resource, err := store.ProjectResourceByName(context.Background(), project.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	if resource.ID != "service-id" || resource.Kind != "service" || resource.Name != "api" {
		t.Fatalf("resource = %+v", resource)
	}
	if _, err := store.ProjectByName(context.Background(), "missing"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project error = %v", err)
	}
	if _, err := store.ProjectResourceByName(context.Background(), project.ID, "missing"); !errors.Is(err, ErrProjectResourceNotFound) {
		t.Fatalf("missing resource error = %v", err)
	}
}
