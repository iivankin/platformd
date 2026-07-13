package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalAuditStoresLifecycleMetadataWithoutTerminalContent(t *testing.T) {
	t.Parallel()

	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exitCode := 0
	err = store.AppendTerminalAudit(context.Background(), TerminalAuditInput{
		ID: "audit", ActorID: "subject", ActorEmail: "admin@example.com",
		Action: "container_terminal.end", TargetKind: "service", TargetID: "service",
		ProjectID: "project", ServiceID: "service", ContainerID: "container",
		Command: []string{"/bin/sh"}, SourceIP: "203.0.113.9", Result: "succeeded",
		StartedAtMillis: 1_000, FinishedAtMillis: 2_500, DurationMillis: 1_500,
		CloseReason: "client_closed", ExitCode: &exitCode, CreatedAtMillis: 2_500,
	})
	if err != nil {
		t.Fatal(err)
	}
	var action, metadata string
	if err := store.QueryRowContext(context.Background(), "SELECT action, metadata_json FROM audit_events WHERE id = 'audit'").Scan(&action, &metadata); err != nil {
		t.Fatal(err)
	}
	if action != "container_terminal.end" || !strings.Contains(metadata, `"durationMillis":1500`) || !strings.Contains(metadata, `"closeReason":"client_closed"`) || strings.Contains(metadata, "keystroke") || strings.Contains(metadata, "output") {
		t.Fatalf("terminal audit = %s %s", action, metadata)
	}
}
