package state_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestManagedPostgresExtensionRecipeIsDurableAndCascades(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project-extension", Name: "extensions", AuditEventID: "project-extension-audit",
		ActorID: "user", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedPostgres(ctx, state.CreateManagedPostgres{
		ID: "postgres-extension", ProjectID: "project-extension", Name: "database", ImageTag: "18.3-bookworm",
		ImageDigest: managedPostgresTestDigest, VolumeID: "volume-extension", DatabaseName: "app",
		OwnerUsername: "owner", OwnerPasswordEncrypted: []byte("owner"), BootstrapPasswordEncrypted: []byte("bootstrap"),
		AuditEventID: "postgres-extension-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutManagedPostgresExtension(ctx, state.PutManagedPostgresExtension{
		PostgresID: "postgres-extension", Name: "vector", Version: "0.8.5",
		RecipeDigest: managedPostgresTestDigest, TimestampMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	extensions, err := store.ManagedPostgresExtensions(ctx, "postgres-extension")
	if err != nil || len(extensions) != 1 || extensions[0].Name != "vector" || extensions[0].Version != "0.8.5" || extensions[0].RecipeDigest != managedPostgresTestDigest {
		t.Fatalf("extensions = %+v, %v", extensions, err)
	}
	if err := store.DeleteManagedPostgresExtension(ctx, "postgres-extension", "vector"); err != nil {
		t.Fatal(err)
	}
	extensions, err = store.ManagedPostgresExtensions(ctx, "postgres-extension")
	if err != nil || len(extensions) != 0 {
		t.Fatalf("extensions after delete = %+v, %v", extensions, err)
	}
	if err := store.PutManagedPostgresExtension(ctx, state.PutManagedPostgresExtension{
		PostgresID: "postgres-extension", Name: "vector", Version: "0.8.5",
		RecipeDigest: managedPostgresTestDigest, TimestampMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "DELETE FROM managed_postgres WHERE id = ?", "postgres-extension")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	all, err := store.AllManagedPostgresExtensions(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("extensions after resource delete = %+v, %v", all, err)
	}
}
