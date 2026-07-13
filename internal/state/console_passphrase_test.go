package state_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestResetConsolePassphraseUpdatesOnlyVerifierAndAuditsLocalRoot(t *testing.T) {
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	input := state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$old", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("encrypted"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}
	if err := store.CreateInstallation(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetConsolePassphrase(ctx, state.ResetConsolePassphrase{
		Verifier: "$argon2id$new", AuditEventID: "reset-audit", ResetAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if installation.ConsolePassphrasePHC != "$argon2id$new" || installation.AdminHostname != input.AdminHostname ||
		installation.AccessAudience != input.AccessAudience || installation.UpdatedAtMillis != 2 {
		t.Fatalf("installation after reset = %+v", installation)
	}
	var actorKind, actorID, action, targetID, metadata string
	if err := store.QueryRowContext(ctx, `
SELECT actor_kind, actor_id, action, target_id, metadata_json
FROM audit_events WHERE id = 'reset-audit'`).Scan(&actorKind, &actorID, &action, &targetID, &metadata); err != nil {
		t.Fatal(err)
	}
	if actorKind != "local_root" || actorID != "init" || action != "installation.console_passphrase_reset" ||
		targetID != input.ID || metadata != "{}" {
		t.Fatalf("reset audit = %q/%q/%q/%q/%q", actorKind, actorID, action, targetID, metadata)
	}
}
