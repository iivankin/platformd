package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type resourceExporterStub struct {
	payload []byte
	err     error
	calls   int
}

func (exporter *resourceExporterStub) Export(context.Context, string) (ResourceExport, error) {
	exporter.calls++
	if exporter.err != nil {
		return ResourceExport{}, exporter.err
	}
	return ResourceExport{Reader: io.NopCloser(bytes.NewReader(exporter.payload))}, nil
}

func TestResourceJobPublishesRetentionAndSuccessfulRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	remote := newMemoryControlRemote()
	old := resourcePublicationBuild(t, master, "redis", "redis-1", "old-generation", []byte("old"), time.Unix(10, 0))
	if err := PublishResource(ctx, remote, master, old); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(old.WorkDirectory)
	exporter := &resourceExporterStub{payload: []byte("redis-rdb")}
	clock := time.Unix(20, 0)
	job, err := NewResourceJob(ResourceJobConfig{
		Store: store, Target: target, TargetGate: targetGate, Admission: admission.New(), Growth: growthStub{},
		Master: master, WorkRoot: filepath.Join(root, "work"), Exporters: map[string]ResourceExporter{"redis": exporter},
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return remote, nil },
		Now:           func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	occurrence := int64(19_000)
	record, err := job.RunResource(ctx, "redis", "redis-1", "target", &occurrence, 1)
	if err != nil {
		t.Fatal(err)
	}
	if exporter.calls != 1 || record.Status != "succeeded" || record.SizeBytes == nil || *record.SizeBytes <= 0 ||
		record.ScheduledOccurrenceMillis == nil || *record.ScheduledOccurrenceMillis != occurrence {
		t.Fatalf("resource backup record = %+v, exporter calls = %d", record, exporter.calls)
	}
	generations, err := ListResourceGenerations(ctx, remote, "redis", "redis-1")
	if err != nil || len(generations) != 1 || generations[0].GenerationID != record.GenerationID {
		t.Fatalf("retained generations = %+v, %v", generations, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "work"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("resource work directory = %v, %v", entries, err)
	}
}

func TestResourceJobRecordsExporterFailureAndSkipsRecordWhenTargetBusy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	exporter := &resourceExporterStub{err: errors.New("snapshot failed")}
	job, err := NewResourceJob(ResourceJobConfig{
		Store: store, Target: target, TargetGate: targetGate, Admission: admission.New(), Growth: growthStub{},
		Master: master, WorkRoot: filepath.Join(root, "work"), Exporters: map[string]ResourceExporter{"registry": exporter},
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return newMemoryControlRemote(), nil },
		Now:           func() time.Time { return time.Unix(30, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := job.RunResource(ctx, "registry", "repository-1", "target", nil, 7)
	if err == nil || record.Status != "failed" || record.ErrorCode != "registry_backup_failed" {
		t.Fatalf("failed resource backup = %+v, %v", record, err)
	}
	release, acquired := targetGate.TryAcquire()
	if !acquired {
		t.Fatal("failed to occupy target gate")
	}
	defer release()
	if _, err := job.RunResource(ctx, "registry", "repository-1", "target", nil, 7); !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("busy resource backup error = %v", err)
	}
	var count int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM backups").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("busy request created a backup record; count = %d", count)
	}
}

func resourceJobTarget(t *testing.T, root string) (*state.Store, *TargetApplication, *Gate, cryptobox.MasterKey) {
	t.Helper()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(root, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	createBackupInstallation(t, store)
	master := cryptobox.MasterKey{1, 2, 3, 4}
	sealedSecret, err := SealTargetSecret(master, "installation-id", "secret")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.SetBackupTarget(ctx, state.SetBackupTarget{
		Target: state.BackupTarget{
			ID: "target", Name: "Primary", Endpoint: "https://s3.example.com", Region: "us-east-1",
			Bucket: "bucket", Prefix: "prefix", AccessKeyID: "access", SecretAccessKeyEncrypted: sealedSecret,
		},
		AuditEventID: "target-audit", ActorKind: "access", ActorID: "test", ActorEmail: "test@example.com",
		UpdatedAtMillis: time.Unix(5, 0).UnixMilli(),
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	gate := NewGate()
	target, err := NewTargetApplication(store, master, gate, func(remotes3.Config) (Probe, error) {
		return &probeStub{}, nil
	}, nil, func() time.Time { return time.Unix(5, 0) })
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, target, gate, master
}
