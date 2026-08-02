package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectWebhookLifecyclePersistsConfigurationAndAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec("INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'storefront', 1, 1)"); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateProjectWebhook(ctx, ProjectWebhookMutation{
		Webhook: ProjectWebhook{
			ID: "webhook", ProjectID: "project", URL: "https://example.com/first",
			EventTypes: []string{"deployment.failed"}, CreatedAtMillis: 2, UpdatedAtMillis: 2,
		},
		AuditEventID: "audit-create", ActorID: "actor", ActorEmail: "actor@example.com",
		RequestCorrelationID: "request-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.URL != "https://example.com/first" {
		t.Fatalf("created webhook = %+v", created)
	}

	updated, err := store.UpdateProjectWebhook(ctx, ProjectWebhookMutation{
		Webhook: ProjectWebhook{
			ID: "webhook", ProjectID: "project", URL: "https://example.com/updated",
			EventTypes: []string{"deployment.failed", "deployment.interrupted"}, UpdatedAtMillis: 3,
		},
		AuditEventID: "audit-update", ActorID: "actor", ActorEmail: "actor@example.com",
		RequestCorrelationID: "request-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAtMillis != 2 || updated.UpdatedAtMillis != 3 {
		t.Fatalf("updated webhook timestamps = %d/%d", updated.CreatedAtMillis, updated.UpdatedAtMillis)
	}
	webhooks, err := store.ProjectWebhooks(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 1 || webhooks[0].URL != "https://example.com/updated" || len(webhooks[0].EventTypes) != 2 {
		t.Fatalf("listed webhooks = %+v", webhooks)
	}

	if err := store.DeleteProjectWebhook(ctx, DeleteProjectWebhook{
		ID: "webhook", ProjectID: "project", AuditEventID: "audit-delete",
		ActorID: "actor", ActorEmail: "actor@example.com",
		RequestCorrelationID: "request-delete", DeletedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	webhooks, err = store.ProjectWebhooks(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 0 {
		t.Fatalf("deleted webhook still listed: %+v", webhooks)
	}
	var auditCount int
	if err := store.database.QueryRow("SELECT count(*) FROM audit_events WHERE target_id = 'webhook'").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("webhook audit count = %d, want 3", auditCount)
	}
	var deletedMetadata string
	if err := store.database.QueryRow("SELECT metadata_json FROM audit_events WHERE id = 'audit-delete'").Scan(&deletedMetadata); err != nil {
		t.Fatal(err)
	}
	if deletedMetadata != `{"actorEmail":"actor@example.com","projectId":"project","url":"https://example.com/updated"}` {
		t.Fatalf("deleted webhook audit metadata = %s", deletedMetadata)
	}
}
