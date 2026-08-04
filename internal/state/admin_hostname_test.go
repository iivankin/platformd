package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestAdminHostnameRequiresCertificateAndUnusedPublicRole(t *testing.T) {
	store := openStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	certificate := stateTestCertificate(t, []string{"admin.example.com", "control.example.com", "api.example.com"})
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation-a", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate-a",
		OriginCertificatePEM: certificate, OriginPrivateKey: []byte("encrypted-a"),
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	input := state.SetAdminHostnameInput{
		Hostname: "uncovered.example.com", AuditEventID: "audit-admin-1",
		ActorID: "subject-a", ActorEmail: "admin@example.com",
		RequestCorrelationID: "request-a", UpdatedAtMillis: 2,
	}
	if err := store.SetAdminHostname(ctx, input); !errors.Is(err, state.ErrCertificateCoverage) {
		t.Fatalf("uncovered admin hostname error = %v", err)
	}
	input.Hostname = "CONTROL.Example.com"
	input.AuditEventID = "audit-admin-2"
	input.UpdatedAtMillis = 3
	if err := store.SetAdminHostname(ctx, input); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(ctx)
	if err != nil || installation.AdminHostname != "control.example.com" {
		t.Fatalf("installation admin hostname = %q, %v", installation.AdminHostname, err)
	}
}
