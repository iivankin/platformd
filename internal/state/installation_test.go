package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestCreateAndLoadInstallationAtomically(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	defer store.Close()
	ctx := context.Background()
	automationHostname := "admin-api.example.com"
	input := state.InitialInstallation{
		ID:                   "installation-id",
		AdminHostname:        "admin.example.com",
		AutomationHostname:   &automationHostname,
		AccessTeamDomain:     "team.cloudflareaccess.com",
		AccessAudience:       "audience",
		ConsolePassphrasePHC: "$argon2id$verifier",
		OriginCertificateID:  "certificate-id",
		OriginCertificatePEM: "certificate",
		OriginPrivateKey:     []byte("encrypted-key"),
		InitialAuditEventID:  "audit-id",
		CreatedAtMillis:      42,
	}
	if err := store.CreateInstallation(ctx, input); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if installation.ID != input.ID || installation.AdminHostname != input.AdminHostname {
		t.Fatalf("installation = %+v", installation)
	}
	if installation.AutomationHostname == nil || *installation.AutomationHostname != automationHostname {
		t.Fatalf("automation hostname = %v", installation.AutomationHostname)
	}
	if len(installation.OriginCertificates) != 1 || string(installation.OriginCertificates[0].PrivateKeyEncrypted) != "encrypted-key" {
		t.Fatalf("Origin certificates = %+v", installation.OriginCertificates)
	}
	var auditCount int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE target_id = ?", input.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d", auditCount)
	}
	if err := store.CreateInstallation(ctx, input); !errors.Is(err, state.ErrAlreadyInitialized) {
		t.Fatalf("second create error = %v", err)
	}
}

func TestInstallationReturnsExplicitNotInitialized(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	defer store.Close()
	if _, err := store.Installation(context.Background()); !errors.Is(err, state.ErrNotInitialized) {
		t.Fatalf("error = %v", err)
	}
}
