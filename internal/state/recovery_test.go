package state

import (
	"context"
	"errors"
	"testing"
)

func TestCompleteRecoverySwitchesModeAndAuditAtomically(t *testing.T) {
	t.Parallel()
	store := openPersistentVolumeStore(t)
	defer store.Close()
	seedRecoveryInstallation(t, store)

	if err := store.CompleteRecovery(context.Background(), CompleteRecovery{
		InstallationID: "installation", AuditEventID: "complete-audit", CompletedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(context.Background())
	if err != nil || installation.RecoveryMode || installation.UpdatedAtMillis != 3 {
		t.Fatalf("completed installation = %+v, %v", installation, err)
	}
	var actorKind, actorID, action string
	if err := store.QueryRowContext(context.Background(), `
SELECT actor_kind, actor_id, action FROM audit_events WHERE id = 'complete-audit'`).Scan(
		&actorKind, &actorID, &action,
	); err != nil {
		t.Fatal(err)
	}
	if actorKind != "system" || actorID != "disaster_restore" || action != "recovery.complete" {
		t.Fatalf("recovery audit = %q/%q/%q", actorKind, actorID, action)
	}
	if err := store.CompleteRecovery(context.Background(), CompleteRecovery{
		InstallationID: "installation", AuditEventID: "second-audit", CompletedAtMillis: 4,
	}); !errors.Is(err, ErrRecoveryNotActive) {
		t.Fatalf("second completion error = %v", err)
	}
	var count int
	if err := store.QueryRowContext(context.Background(), `
SELECT count(*) FROM audit_events WHERE id = 'second-audit'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed recovery completion committed audit")
	}
}

func seedRecoveryInstallation(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateInstallation(ctx, InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("sealed"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.ExecContext(ctx, `
UPDATE installation SET recovery_mode = 1, updated_at = 2 WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
}
