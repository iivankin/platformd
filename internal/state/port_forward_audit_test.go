package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortForwardTicketAuditDoesNotStoreBearerSecret(t *testing.T) {
	t.Parallel()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.RecordPortForwardTicket(context.Background(), RecordPortForwardTicket{
		AuditEventID: "ticket-id", ActorTokenID: "admin-token", TicketID: "ticket-id",
		ProjectID: "project", ResourceKind: "postgres", ResourceID: "database", Port: 5432,
		CreatedAtMillis: 1_000, ExpiresAtMillis: 61_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var action, targetKind, metadata string
	if err := store.QueryRowContext(context.Background(), `
SELECT action, target_kind, metadata_json FROM audit_events WHERE id = 'ticket-id'`,
	).Scan(&action, &targetKind, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "port_forward.ticket.create" || targetKind != "port_forward_ticket" ||
		!strings.Contains(metadata, `"resourceId":"database"`) || strings.Contains(metadata, "pft_") {
		t.Fatalf("port forward audit = %s / %s / %s", action, targetKind, metadata)
	}
}
