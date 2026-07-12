package state_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestRuntimeProjectsAreStableAndIncludeObjectStoreCapability(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		statements := []string{
			"INSERT INTO projects(id, name, created_at, updated_at) VALUES ('z', 'zeta', 1, 1)",
			"INSERT INTO projects(id, name, created_at, updated_at) VALUES ('a', 'alpha', 1, 1)",
			"INSERT INTO object_stores(id, project_id, name, bucket_name, created_at, updated_at) VALUES ('store', 'z', 'assets', 'assets', 1, 1)",
		}
		for _, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	projects, err := store.RuntimeProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].ID != "a" || projects[0].ObjectStoreEnabled || projects[1].ID != "z" || !projects[1].ObjectStoreEnabled {
		t.Fatalf("unexpected runtime projects: %+v", projects)
	}
}
