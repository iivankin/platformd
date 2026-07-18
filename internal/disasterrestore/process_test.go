package disasterrestore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/disasterrestore"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/state"
)

func TestMain(main *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "__restore-import" {
		file := os.NewFile(3, "restore-import-test")
		if file == nil {
			_, _ = fmt.Fprintln(os.Stderr, "missing restore input fd")
			os.Exit(1)
		}
		payload, err := disasterrestore.ReadImportPayload(file)
		if err == nil {
			var result disasterrestore.ImportResult
			result, err = disasterrestore.ImportSnapshot(context.Background(), payload)
			if err == nil {
				err = json.NewEncoder(os.Stdout).Encode(result)
			}
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(main.Run())
}

func TestRunExactImporterTransfersBoundedPayloadThroughPrivateFD(t *testing.T) {
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
	if _, err := store.SetBackupTarget(ctx, state.SetBackupTarget{
		Target: state.BackupTarget{
			ID: "recovery-target", Name: "Offsite", Endpoint: "https://s3.example.com", Region: "region",
			Bucket: "bucket", Prefix: "prefix", AccessKeyID: "old-access",
			SecretAccessKeyEncrypted: []byte("old-secret"),
		},
		AuditEventID: "target-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	payload := disasterrestore.ImportPayload{
		DatabasePath: path, ExpectedInstallationID: "installation", ExpectedSchemaVersion: state.SupportedSchemaVersion(),
		MasterRecoveryKey: masterkey.RecoveryString(cryptobox.MasterKey{1, 2, 3}),
		Remote: disasterrestore.ImportTarget{
			Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket", Prefix: "prefix",
			AccessKeyID: "access", SecretAccessKey: "secret",
		},
		AuditEventID: "restore-audit", ImportedAtMillis: 2, ExpectedUID: os.Geteuid(),
	}
	executable := os.Getenv("PLATFORMD_EXACT_IMPORTER")
	if executable == "" {
		executable = os.Args[0]
	}
	result, err := disasterrestore.RunExactImporter(ctx, executable, payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.AdminHostname != "admin.example.com" || result.OriginCertificatePEM != certificate {
		t.Fatalf("exact importer result = %+v", result)
	}
}
