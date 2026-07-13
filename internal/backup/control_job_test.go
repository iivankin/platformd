package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type growthStub struct{ err error }

func (growth growthStub) PermitGrowth(context.Context) error { return growth.err }

func TestControlJobPublishesAndRecordsOnlyStartedWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := state.Open(ctx, filepath.Join(root, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createBackupInstallation(t, store)
	paths, publicKey := controlReleaseSlot(t, root)
	master := cryptobox.MasterKey{1, 2, 3}
	targetGate := NewGate()
	targetApplication, err := NewTargetApplication(store, master, targetGate, func(remotes3.Config) (Probe, error) {
		return &probeStub{}, nil
	}, rand.Reader, func() time.Time { return time.Unix(5, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := targetApplication.SetTarget(ctx, TargetInput{
		Endpoint: "https://s3.example.com", Region: "region", Bucket: "bucket", Prefix: "prefix",
		AccessKeyID: "access", SecretAccessKey: "secret",
		Actor: Actor{Kind: "access", ID: "test", Email: "test@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	remote := newMemoryControlRemote()
	job, err := NewControlJob(ControlJobConfig{
		Store: store, Target: targetApplication, TargetGate: targetGate, Admission: admission.New(), Growth: growthStub{},
		Master: master, InstallationID: "installation-id", WorkRoot: filepath.Join(root, "work"), ExpectedUID: os.Geteuid(),
		PublicKey: publicKey, ReleaseSlot: func() (string, error) { return filepath.Join(paths.ReleasesRoot, "1.2.3"), nil },
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return remote, nil },
		Now:           func() time.Time { return time.Unix(10, 0) }, Random: bytes.NewReader(bytes.Repeat([]byte{0x44}, 4096)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := job.RunControl(ctx); err != nil {
		t.Fatal(err)
	}
	completion, exists, err := currentControlCompletion(ctx, remote)
	if err != nil || !exists || completion.InstallationID != "installation-id" {
		t.Fatalf("published completion = %+v, %v, %v", completion, exists, err)
	}
	var backupID string
	if err := store.QueryRowContext(ctx, "SELECT id FROM backups").Scan(&backupID); err != nil {
		t.Fatal(err)
	}
	record, err := store.Backup(ctx, backupID)
	if err != nil || record.Status != "succeeded" || record.SizeBytes == nil || *record.SizeBytes <= 0 {
		t.Fatalf("control backup record = %+v, %v", record, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "work"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("control work directory = %v, %v", entries, err)
	}
}
