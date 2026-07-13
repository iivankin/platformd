package state_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestImportControlAtomicallyReplacesTargetAndEntersRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := createTestInstallation(ctx, store); err != nil {
		t.Fatal(err)
	}
	team := "replacement.cloudflareaccess.com"
	audience := "replacement-audience"
	secret := []byte("encrypted-secret")
	if err := store.ImportControl(ctx, state.ControlImport{
		ExpectedInstallationID: "installation", AccessTeamDomain: &team, AccessAudience: &audience,
		Target: state.BackupTarget{
			Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket", Prefix: "prefix",
			AccessKeyID: "access", SecretAccessKeyEncrypted: secret,
		},
		AuditEventID: "restore-audit", ImportedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(ctx)
	if err != nil || !installation.RecoveryMode || installation.AccessTeamDomain != team || installation.AccessAudience != audience {
		t.Fatalf("restored installation = %+v, %v", installation, err)
	}
	target, err := store.BackupTarget(ctx)
	if err != nil || target.Endpoint != "https://s3.example.com" || !bytes.Equal(target.SecretAccessKeyEncrypted, secret) {
		t.Fatalf("restored target = %+v, %v", target, err)
	}
	var action string
	if err := store.QueryRowContext(ctx, "SELECT action FROM audit_events WHERE id = 'restore-audit'").Scan(&action); err != nil || action != "control.restore" {
		t.Fatalf("restore audit = %q, %v", action, err)
	}
}

func TestImportControlMismatchRollsBackEveryMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := createTestInstallation(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportControl(ctx, state.ControlImport{
		ExpectedInstallationID: "other",
		Target:                 state.BackupTarget{Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket", AccessKeyID: "access", SecretAccessKeyEncrypted: []byte("secret")},
		AuditEventID:           "restore-audit", ImportedAtMillis: 2,
	}); err == nil {
		t.Fatal("mismatched installation was imported")
	}
	installation, err := store.Installation(ctx)
	if err != nil || installation.RecoveryMode {
		t.Fatalf("failed import changed recovery state = %+v, %v", installation, err)
	}
	if _, err := store.BackupTarget(ctx); err != state.ErrBackupTargetNotFound {
		t.Fatalf("failed import wrote target: %v", err)
	}
}

func createTestInstallation(ctx context.Context, store *state.Store) error {
	return store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com", AccessTeamDomain: "team.cloudflareaccess.com",
		AccessAudience: "audience", ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("encrypted-key"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	})
}
