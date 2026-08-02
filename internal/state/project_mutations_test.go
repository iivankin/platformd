package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestCreateProjectCommitsResourceAndAuditTogether(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	input := state.CreateProject{
		ID: "project-id", Name: "shop", AuditEventID: "audit-id",
		ActorID: "access-subject", ActorEmail: "admin@example.com",
		RequestCorrelationID: "request-id", CreatedAtMillis: 42,
	}
	created, err := store.CreateProject(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != input.ID || created.Name != input.Name {
		t.Fatalf("created project = %+v", created)
	}
	projects, err := store.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0] != created {
		t.Fatalf("projects = %+v, %v", projects, err)
	}
	var projectID, actorID, correlationID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT project_id, actor_id, request_correlation_id, metadata_json
FROM audit_events WHERE id = ?`, input.AuditEventID).Scan(&projectID, &actorID, &correlationID, &metadata); err != nil {
		t.Fatal(err)
	}
	if projectID != input.ID || actorID != input.ActorID || correlationID != input.RequestCorrelationID || metadata != `{"actorEmail":"admin@example.com","name":"shop"}` {
		t.Fatalf("audit = %q, %q, %q, %q", projectID, actorID, correlationID, metadata)
	}
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "other-id", Name: input.Name, AuditEventID: "other-audit",
		ActorID: input.ActorID, ActorEmail: input.ActorEmail, CreatedAtMillis: 43,
	}); !errors.Is(err, state.ErrProjectNameConflict) {
		t.Fatalf("duplicate project error = %v", err)
	}
	var auditCount int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM audit_events").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("failed duplicate wrote audit event: count=%d", auditCount)
	}
}

func TestCreateProjectRejectsNonDNSNameBeforeWrite(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project-id", Name: "Not valid", AuditEventID: "audit-id",
		ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: 1,
	}); err == nil {
		t.Fatal("expected invalid project name to fail")
	}
	var count int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM projects").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid project was persisted: count=%d err=%v", count, err)
	}
}

func TestCreateProjectByTokenRecordsTokenActorWithoutFakeEmail(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	input := state.CreateProjectByToken{
		ID: "project-id", Name: "shop", AuditEventID: "audit-id",
		ActorTokenID: "token-id", RequestCorrelationID: "request-id", CreatedAtMillis: 42,
	}
	if _, err := store.CreateProjectByToken(ctx, input); err != nil {
		t.Fatal(err)
	}
	var projectID, actorKind, actorID, correlationID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT project_id, actor_kind, actor_id, request_correlation_id, metadata_json
FROM audit_events WHERE id = ?`, input.AuditEventID).Scan(&projectID, &actorKind, &actorID, &correlationID, &metadata); err != nil {
		t.Fatal(err)
	}
	if projectID != input.ID || actorKind != "token" || actorID != input.ActorTokenID || correlationID != input.RequestCorrelationID || metadata != `{"name":"shop"}` {
		t.Fatalf("token audit = %q, %q, %q, %q, %q", projectID, actorKind, actorID, correlationID, metadata)
	}
}
