package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordServerExecExcludesCommandAndOutput(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordServerExec(context.Background(), RecordServerExec{
		AuditEventID: "audit", ActorTokenID: "token", RequestCorrelationID: "request",
		Succeeded: false, StartedAtMillis: 10, FinishedAtMillis: 20, DurationMillis: 10,
		ExitCode: 9, TimedOut: true, StdoutTruncated: true,
	}); err != nil {
		t.Fatal(err)
	}
	var actorKind, actorID, action, targetKind, targetID, result, metadata string
	if err := store.QueryRowContext(context.Background(), `
SELECT actor_kind, actor_id, action, target_kind, target_id, result, metadata_json
FROM audit_events WHERE id = 'audit'`).Scan(&actorKind, &actorID, &action, &targetKind, &targetID, &result, &metadata); err != nil {
		t.Fatal(err)
	}
	if actorKind != "token" || actorID != "token" || action != "server.exec" || targetKind != "server" || targetID != "host" || result != "failed" {
		t.Fatalf("audit = %s/%s %s %s/%s %s", actorKind, actorID, action, targetKind, targetID, result)
	}
	if !strings.Contains(metadata, `"timedOut":true`) || !strings.Contains(metadata, `"stdoutTruncated":true`) || strings.Contains(metadata, "command") || strings.Contains(metadata, "output") {
		t.Fatalf("audit metadata = %s", metadata)
	}
}
