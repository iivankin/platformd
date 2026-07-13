package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type probeStub struct {
	err   error
	calls int
}

func (probe *probeStub) Probe(context.Context) error {
	probe.calls++
	return probe.err
}

func TestTargetProbePrecedesEncryptedCommitAndFailedReplacePreservesOldTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createBackupInstallation(t, store)
	master := cryptobox.MasterKey{1, 2, 3}
	gate := NewGate()
	probe := &probeStub{}
	var probed remotes3.Config
	clock := time.UnixMilli(1_720_000_000_000)
	application, err := NewTargetApplication(
		store, master, gate,
		func(config remotes3.Config) (Probe, error) {
			probed = config
			return probe, nil
		},
		bytes.NewReader(sequenceBytes(100)),
		func() time.Time { return clock },
	)
	if err != nil {
		t.Fatal(err)
	}
	input := TargetInput{
		Endpoint: "https://S3.Example.com/", Region: "eu-central-003", Bucket: "backup-bucket",
		Prefix: "/platformd/test/", AccessKeyID: "access-key", SecretAccessKey: "secret-key",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	}
	result, err := application.SetTarget(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 || probed.Prefix != "platformd/test" || result.RequestID == "" ||
		result.Target.Endpoint != "https://s3.example.com" || result.Target.AccessKeyID != input.AccessKeyID {
		t.Fatalf("target result = %+v, probed = %+v, calls = %d", result, probed, probe.calls)
	}
	stored, err := store.BackupTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.SecretAccessKeyEncrypted, []byte(input.SecretAccessKey)) {
		t.Fatal("backup target secret was stored in plaintext")
	}
	runtimeTarget, err := application.RuntimeTarget(ctx)
	if err != nil || runtimeTarget.SecretAccessKey != input.SecretAccessKey {
		t.Fatalf("runtime target = %+v, %v", runtimeTarget, err)
	}

	probe.err = errors.New("remote unavailable")
	input.Endpoint = "https://replacement.example.com"
	if _, err := application.SetTarget(ctx, input); err == nil {
		t.Fatal("failed capability probe was accepted")
	}
	current, configured, err := application.Target(ctx)
	if err != nil || !configured || current.Endpoint != result.Target.Endpoint {
		t.Fatalf("target after failed replace = %+v configured=%t err=%v", current, configured, err)
	}

	release, acquired := gate.TryAcquire()
	if !acquired {
		t.Fatal("failed to occupy backup target gate")
	}
	if _, err := application.DeleteTarget(ctx, input.Actor); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("busy delete error = %v", err)
	}
	release()
	requestID, err := application.DeleteTarget(ctx, input.Actor)
	if err != nil || requestID == "" {
		t.Fatalf("delete target = %q, %v", requestID, err)
	}
	if _, configured, err := application.Target(ctx); err != nil || configured {
		t.Fatalf("deleted target configured=%t err=%v", configured, err)
	}
}

func TestEmbeddedPublicObjectStoreCannotBecomeBackupTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createBackupInstallation(t, store)
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "demo", AuditEventID: "project-audit",
		ActorID: "user", ActorEmail: "admin@example.com", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateObjectStore(ctx, state.CreateObjectStore{
		ID: "store", ProjectID: "project", Name: "assets", BucketName: "assets-bucket",
		PublicHostname: "objects.example.com", CORSOrigins: []string{},
		CredentialID: "credential", CredentialName: "default",
		CredentialPermission: "read_write", CredentialSecret: []byte("encrypted"),
		AuditEventID: "store-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 11,
	}); err != nil {
		t.Fatal(err)
	}
	application, err := NewTargetApplication(
		store, cryptobox.MasterKey{1}, nil,
		func(remotes3.Config) (Probe, error) {
			t.Fatal("embedded target reached capability probe")
			return nil, nil
		},
		bytes.NewReader(bytes.Repeat([]byte{0x24}, 40)), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.SetTarget(ctx, TargetInput{
		Endpoint: "https://objects.example.com", Region: "us-east-1", Bucket: "backup-bucket",
		AccessKeyID: "access", SecretAccessKey: "secret",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if !errors.Is(err, ErrEmbeddedTarget) {
		t.Fatalf("embedded target error = %v", err)
	}
}

func createBackupInstallation(t *testing.T, store *state.Store) {
	t.Helper()
	if err := store.CreateInstallation(context.Background(), state.InitialInstallation{
		ID: "installation-id", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("encrypted-key"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func sequenceBytes(count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = byte(index)
	}
	return result
}
