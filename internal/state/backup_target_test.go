package state_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestClearControlBackupTargetRecordsInstallationAuditTarget(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("sealed"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.SetControlBackupTarget(ctx, state.SetControlBackupTarget{
		AuditEventID: "clear-audit", ActorKind: "access", ActorID: "subject",
		ActorEmail: "admin@example.com", RequestCorrelationID: "request",
		UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}

	var targetKind, targetID string
	if err := store.QueryRowContext(ctx, `
SELECT target_kind, target_id FROM audit_events WHERE id = 'clear-audit'`).Scan(&targetKind, &targetID); err != nil {
		t.Fatal(err)
	}
	if targetKind != "installation" || targetID != "installation" {
		t.Fatalf("control target audit target = %q/%q", targetKind, targetID)
	}
}
