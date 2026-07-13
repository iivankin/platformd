package disasterrestore_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/disasterrestore"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/state"
)

func TestImportSnapshotUsesExactSchemaAndOneTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platformd.db")
	store, err := state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	certificate := restoreCertificate(t, "admin.example.com")
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com", AccessTeamDomain: "team.cloudflareaccess.com",
		AccessAudience: "audience", ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: certificate, OriginPrivateKey: []byte("encrypted"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	result, err := disasterrestore.ImportSnapshot(ctx, disasterrestore.ImportPayload{
		DatabasePath: path, ExpectedInstallationID: "installation", ExpectedSchemaVersion: state.SupportedSchemaVersion(),
		MasterRecoveryKey: masterkey.RecoveryString(master),
		Remote:            disasterrestore.ImportTarget{Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket", Prefix: "prefix", AccessKeyID: "access", SecretAccessKey: "secret"},
		AuditEventID:      "restore-audit", ImportedAtMillis: 2, ExpectedUID: os.Geteuid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AdminHostname != "admin.example.com" || result.OriginCertificatePEM != certificate {
		t.Fatalf("import result = %+v", result)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("SQLite sidecar %s survived exact import: %v", suffix, err)
		}
	}
	store, err = state.Open(ctx, path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	installation, err := store.Installation(ctx)
	if err != nil || !installation.RecoveryMode {
		t.Fatalf("imported installation = %+v, %v", installation, err)
	}
	target, err := store.BackupTarget(ctx)
	if err != nil || target.Endpoint != "https://s3.example.com" || len(target.SecretAccessKeyEncrypted) == 0 {
		t.Fatalf("imported target = %+v, %v", target, err)
	}
	application, err := backup.NewTargetApplication(store, master, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeTarget, err := application.RuntimeTarget(ctx)
	if err != nil || runtimeTarget.SecretAccessKey != "secret" {
		t.Fatalf("decrypted imported target = %+v, %v", runtimeTarget, err)
	}
}

func restoreCertificate(t *testing.T, hostname string) string {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
