package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestRegistryHostnameSetConflictAndClear(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	createInstallation(t, store)
	ctx := context.Background()
	input := state.SetRegistryHostnameInput{
		Hostname: "Registry.Example.com", AuditEventID: "audit-registry-1",
		ActorKind: "access", ActorID: "user", ActorEmail: "admin@example.com",
		RequestCorrelationID: "request-1", UpdatedAtMillis: 100,
	}
	hostname, err := store.SetRegistryHostname(ctx, input)
	if err != nil || hostname == nil || *hostname != "registry.example.com" {
		t.Fatalf("set registry hostname = %v, %v", hostname, err)
	}
	installation, err := store.Installation(ctx)
	if err != nil || installation.RegistryHostname == nil || *installation.RegistryHostname != "registry.example.com" {
		t.Fatalf("installation registry hostname = %v, %v", installation.RegistryHostname, err)
	}

	input.Hostname = installation.AdminHostname
	input.AuditEventID = "audit-registry-2"
	input.UpdatedAtMillis++
	if _, err := store.SetRegistryHostname(ctx, input); !errors.Is(err, state.ErrHostnameInUse) {
		t.Fatalf("admin hostname conflict error = %v", err)
	}

	input.Hostname = ""
	input.AuditEventID = "audit-registry-3"
	input.UpdatedAtMillis++
	if hostname, err := store.SetRegistryHostname(ctx, input); err != nil || hostname != nil {
		t.Fatalf("clear registry hostname = %v, %v", hostname, err)
	}
	installation, err = store.Installation(ctx)
	if err != nil || installation.RegistryHostname != nil {
		t.Fatalf("cleared installation hostname = %v, %v", installation.RegistryHostname, err)
	}
}

func createInstallation(t *testing.T, store *state.Store) {
	t.Helper()
	if err := store.CreateInstallation(context.Background(), state.InitialInstallation{
		ID: "installation-id", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "certificate-id",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("encrypted-key"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 42,
	}); err != nil {
		t.Fatal(err)
	}
}
