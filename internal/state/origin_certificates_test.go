package state_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestOriginCertificateMutationsPreservePublicHostnameCoverage(t *testing.T) {
	store := openStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	adminCertificate := stateTestCertificate(t, []string{"admin.example.com"})
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation-a", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate-a",
		OriginCertificatePEM: adminCertificate, OriginPrivateKey: []byte("encrypted-a"),
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAutomationHostname(ctx, state.SetAutomationHostnameInput{
		Hostname: "api.example.com", AuditEventID: "audit-uncovered", ActorID: "subject-a", UpdatedAtMillis: 2,
	}); !errors.Is(err, state.ErrCertificateCoverage) {
		t.Fatalf("uncovered automation hostname error = %v", err)
	}
	combinedCertificate := stateTestCertificate(t, []string{"admin.example.com", "api.example.com"})
	if err := store.AddOriginCertificate(ctx, state.PutOriginCertificateInput{
		Certificate: state.OriginCertificate{
			ID: "certificate-b", CertificatePEM: combinedCertificate,
			PrivateKeyEncrypted: []byte("encrypted-b"), CreatedAtMillis: 3,
		},
		AuditEventID: "audit-add", ActorID: "subject-a", UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAutomationHostname(ctx, state.SetAutomationHostnameInput{
		Hostname: "api.example.com", AuditEventID: "audit-hostname", ActorID: "subject-a", UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOriginCertificate(ctx, state.DeleteOriginCertificateInput{
		CertificateID: "certificate-b", AuditEventID: "audit-delete", ActorID: "subject-a", DeletedAtMillis: 5,
	}); !errors.Is(err, state.ErrCertificateCoverage) {
		t.Fatalf("dependent certificate deletion error = %v", err)
	}
	installation, err := store.Installation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(installation.OriginCertificates) != 2 {
		t.Fatalf("rolled-back certificate count = %d", len(installation.OriginCertificates))
	}
}

func stateTestCertificate(t *testing.T, names []string) string {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
