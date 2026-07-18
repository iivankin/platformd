package daemon

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type emptyRecoveryRemote struct{}

func (emptyRecoveryRemote) Key(value string) string { return value }
func (emptyRecoveryRemote) Put(context.Context, string, io.Reader, int64, string) error {
	return errors.New("unexpected recovery remote write")
}
func (emptyRecoveryRemote) Get(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("unexpected recovery remote read")
}
func (emptyRecoveryRemote) Delete(context.Context, string) error {
	return errors.New("unexpected recovery remote delete")
}
func (emptyRecoveryRemote) List(context.Context, string, string) (remotes3.Page, error) {
	return remotes3.Page{}, errors.New("unexpected recovery remote list")
}

func TestRecoveryPlanRestoresEveryResourceInCanonicalOrderBeforeCompletion(t *testing.T) {
	var events []string
	plan := recoveryPlan{
		resources: func(context.Context) (state.ControlResourceIDs, error) {
			return state.ControlResourceIDs{
				RegistryRepositories: []string{"registry-a", "registry-b"},
				ObjectStores:         []string{"objects"},
				Postgres:             []string{"postgres"},
				Redis:                []string{"redis"},
			}, nil
		},
		restore: func(_ context.Context, kind, resourceID string) error {
			events = append(events, kind+":"+resourceID)
			return nil
		},
		complete: func(context.Context) error {
			events = append(events, "complete")
			return nil
		},
	}
	if err := plan.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"registry:registry-a", "registry:registry-b", "object_store:objects",
		"postgres:postgres", "redis:redis", "complete",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("recovery events = %v, want %v", events, want)
	}
}

func TestRecoveryPlanLeavesRecoveryActiveAfterResourceFailure(t *testing.T) {
	cause := errors.New("corrupt generation")
	completed := false
	plan := recoveryPlan{
		resources: func(context.Context) (state.ControlResourceIDs, error) {
			return state.ControlResourceIDs{Postgres: []string{"database"}}, nil
		},
		restore: func(context.Context, string, string) error { return cause },
		complete: func(context.Context) error {
			completed = true
			return nil
		},
	}
	err := plan.run(context.Background())
	if !errors.Is(err, cause) || completed {
		t.Fatalf("recovery failure = %v, completed=%t", err, completed)
	}
}

func TestRecoveryRejectsAttachmentsForNonObjectResources(t *testing.T) {
	if err := requireNoResourceAttachments(backup.ResourceEnvelope{}); err != nil {
		t.Fatal(err)
	}
	if err := requireNoResourceAttachments(backup.ResourceEnvelope{
		AttachmentCount: 1, AttachmentSize: 10, AttachmentRoot: "checksum",
	}); err == nil {
		t.Fatal("unexpected resource attachments were accepted")
	}
}

func TestDestructiveResourceRestoreRequiresExactConfirmation(t *testing.T) {
	valid := backup.ResourceRestoreOptions{Mode: "replace", DestructiveConfirmed: true}
	if err := requireConfirmedResourceReplacement(valid, "Registry"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []backup.ResourceRestoreOptions{
		{},
		{Mode: "replace"},
		{Mode: "replace", DestructiveConfirmed: true, NewResourceName: "copy"},
		{Mode: "new", DestructiveConfirmed: true},
	} {
		if err := requireConfirmedResourceReplacement(invalid, "Registry"); err == nil {
			t.Fatalf("invalid restore options were accepted: %+v", invalid)
		}
	}
}

func TestRecoveryAttemptCompletesEmptyInstallationAndReleasesGates(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: "certificate", OriginPrivateKey: []byte("sealed"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, "UPDATE installation SET recovery_mode = 1 WHERE singleton = 1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	sealed, err := backup.SealTargetSecret(master, "installation", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBackupTarget(ctx, state.SetBackupTarget{
		Target: state.BackupTarget{
			ID: "target", Name: "Primary",
			Endpoint: "https://s3.example.com", Region: "us-east-1", Bucket: "backups",
			AccessKeyID: "access", SecretAccessKeyEncrypted: sealed,
		},
		AuditEventID: "target-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetControlBackupTarget(ctx, state.SetControlBackupTarget{
		TargetID: "target", AuditEventID: "control-target-audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "admin@example.com", UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	targetGate := backup.NewGate()
	target, err := backup.NewTargetApplication(store, master, targetGate, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutationGate := admission.New()
	installation, err := store.Installation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remoteCreated := false
	attempt, err := newRecoveryAttempt(recoveryConfig{
		Store: store, Target: target, TargetGate: targetGate, Admission: mutationGate,
		Master: master, Installation: installation, Runtime: &runtimeStack{},
		Registry: &registry.Application{}, ObjectStore: &objectstore.Application{},
		Progress: newRecoveryProgress(),
		Remote: func(remotes3.Config) (backup.ControlRemote, error) {
			remoteCreated = true
			return emptyRecoveryRemote{}, nil
		},
		Now:   func() time.Time { return time.UnixMilli(3) },
		NewID: func() (string, error) { return "recovery-audit", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.run(ctx); err != nil {
		t.Fatal(err)
	}
	installation, err = store.Installation(ctx)
	if err != nil || installation.RecoveryMode || !remoteCreated {
		t.Fatalf("completed installation = %+v, remote=%t, err=%v", installation, remoteCreated, err)
	}
	if release, acquired := targetGate.TryAcquire(); !acquired {
		t.Fatal("backup target gate remained held after recovery")
	} else {
		release()
	}
	if lease, err := mutationGate.Begin("test", "after-recovery"); err != nil {
		t.Fatalf("mutation gate remained held after recovery: %v", err)
	} else {
		lease.Release()
	}
}

func TestRecoveryProgressAcceptsSuccessfulManualReplacementWithoutDurableState(t *testing.T) {
	progress := newRecoveryProgress()
	progress.markManual(backup.ResourceRestoreRequest{
		ResourceKind: "redis", ResourceID: "redis-1", GenerationID: "older-generation",
		Options: backup.ResourceRestoreOptions{Mode: "replace", DestructiveConfirmed: true},
		Source:  backup.ResourceRestoreSource{Completion: backup.ResourceCompletion{CompletedAtMillis: 42}},
	})
	progress.markLatest("registry", "registry-1", backup.ResourceCompletion{}, false)
	if !progress.satisfied("redis", "redis-1") || !progress.satisfied("registry", "registry-1") {
		t.Fatal("successful or empty resources were not marked complete")
	}
	results := progress.results()
	if len(results) != 2 || results[0].ResourceKind != "redis" ||
		results[0].GenerationID != "older-generation" || results[0].SourceCompletedAt != 42 ||
		results[1].ResourceKind != "registry" || !results[1].Empty {
		t.Fatalf("recovery progress = %+v", results)
	}
	progress.markManual(backup.ResourceRestoreRequest{
		ResourceKind: "postgres", ResourceID: "copy",
		Options: backup.ResourceRestoreOptions{
			Mode: "replace", DestructiveConfirmed: true, NewResourceName: "copy",
		},
	})
	if progress.satisfied("postgres", "copy") {
		t.Fatal("new-resource restore incorrectly satisfied recovery replacement")
	}
}

func TestRecoveryRetrySignalWakesLoopWithoutDurableQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progress := newRecoveryProgress()
	attempted := make(chan int, 2)
	attempts := 0
	attempt := recoveryAttempt{execute: func(context.Context) error {
		attempts++
		attempted <- attempts
		if attempts == 1 {
			return errors.New("transient failure")
		}
		return nil
	}}
	completed := make(chan struct{})
	startRecoveryLoop(ctx, attempt, progress, func() { close(completed) })
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("first recovery attempt did not run")
	}
	progress.requestRetry()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("manual retry did not wake recovery loop")
	}
	if attempts != 2 {
		t.Fatalf("recovery attempts = %d, want 2", attempts)
	}
}
