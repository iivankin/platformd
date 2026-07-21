package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReferencedContainerImageDigestsUsesTheActiveServiceDeployment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.database.ExecContext(ctx, `
INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'production', 1, 1);
INSERT INTO services(id, project_id, name, source_json, created_at, updated_at)
 VALUES ('service', 'project', 'api', '{"type":"public_image","image":{"reference":"example/api:latest"}}', 1, 1);
INSERT INTO deployments(id, service_id, image_digest, image_reference, service_config_hash, snapshot_json, status, created_at) VALUES
 ('active', 'service', 'sha256:active', 'example/api:latest', 'active', '{}', 'succeeded', 1),
 ('old', 'service', 'sha256:old', 'example/api:old', 'old', '{}', 'succeeded', 0);
UPDATE services SET active_deployment_id = 'active' WHERE id = 'service';`)
	if err != nil {
		t.Fatal(err)
	}

	digests, err := store.ReferencedContainerImageDigests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := digests["sha256:active"]; !ok {
		t.Fatal("active service deployment digest was not retained")
	}
	if _, ok := digests["sha256:old"]; ok {
		t.Fatal("inactive service deployment digest was retained")
	}
}
