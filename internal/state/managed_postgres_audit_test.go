package state_test

import (
	"context"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestManagedPostgresQueryAuditOmitsSQLAndRows(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedPostgres(ctx, state.CreateManagedPostgres{
		ID: "postgres", ProjectID: "project", Name: "database", ImageTag: "17",
		ImageDigest: managedPostgresTestDigest, VolumeID: "volume", DatabaseName: "app",
		OwnerUsername: "owner", OwnerPasswordEncrypted: []byte("owner"),
		BootstrapPasswordEncrypted: []byte("bootstrap"), AuditEventID: "create-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordManagedPostgresQuery(ctx, state.RecordManagedPostgresQuery{
		ResourceID: "postgres", ProjectID: "project", Result: "succeeded", RowCount: 2,
		DurationMillis: 18, AuditEventID: "query-audit", ActorID: "user",
		ActorEmail: "admin@example.com", RequestCorrelationID: "request", CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := store.QueryRowContext(ctx, "SELECT metadata_json FROM audit_events WHERE id = 'query-audit'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != `{"actorEmail":"admin@example.com","durationMillis":"18","rowCount":"2"}` || strings.Contains(metadata, "SELECT") {
		t.Fatalf("query audit metadata = %s", metadata)
	}
}
