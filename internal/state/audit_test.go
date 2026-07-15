package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditPaginationFiltersAndBoundedCleanup(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`
INSERT INTO audit_events(id, actor_kind, actor_id, action, target_kind, target_id, result, metadata_json, created_at) VALUES
('a', 'access', 'user', 'project.create', 'project', 'p', 'succeeded', '{"actorEmail":"a@example.com"}', 10),
('b', 'token', 'token', 'service.update', 'service', 's', 'failed', '{}', 20),
('c', 'token', 'token', 'service.update', 'service', 's', 'succeeded', '{}', 30)`); err != nil {
		t.Fatal(err)
	}
	first, err := store.AuditEvents(context.Background(), AuditQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 2 || first.Events[0].ID != "c" || first.Events[1].ID != "b" || first.NextCursor != "b" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := store.AuditEvents(context.Background(), AuditQuery{Cursor: first.NextCursor, Limit: 2})
	if err != nil || len(second.Events) != 1 || second.Events[0].ID != "a" || second.NextCursor != "" {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	filtered, err := store.AuditEvents(context.Background(), AuditQuery{ActorKind: "token", Action: "service.update", Result: "failed"})
	if err != nil || len(filtered.Events) != 1 || filtered.Events[0].ID != "b" {
		t.Fatalf("filtered page = %+v, %v", filtered, err)
	}
	targeted, err := store.AuditEvents(context.Background(), AuditQuery{TargetKind: "service", TargetID: "s"})
	if err != nil || len(targeted.Events) != 2 || targeted.Events[0].ID != "c" || targeted.Events[1].ID != "b" {
		t.Fatalf("targeted page = %+v, %v", targeted, err)
	}
	deleted, err := store.CleanupAuditEvents(context.Background(), 25, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup = %d, %v", deleted, err)
	}
	remaining, err := store.AuditEvents(context.Background(), AuditQuery{})
	if err != nil || len(remaining.Events) != 2 || remaining.Events[0].ID != "c" || remaining.Events[1].ID != "b" {
		t.Fatalf("remaining = %+v, %v", remaining, err)
	}
}
