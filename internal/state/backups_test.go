package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestBackupRecordTransitionsOnceWithoutDirtyObserver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	dirty := 0
	store.SetControlCommitObserver(func() { dirty++ })
	if err := store.BeginBackup(ctx, state.BeginBackup{
		ID: "backup", TargetID: "target", ResourceKind: "control", ResourceID: "installation",
		GenerationID: "generation", StartedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishBackup(ctx, state.FinishBackup{
		ID: "backup", Status: "succeeded", SizeBytes: 123, FinishedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishBackup(ctx, state.FinishBackup{
		ID: "backup", Status: "failed", ErrorCode: "late", FinishedAtMillis: 3,
	}); !errors.Is(err, state.ErrBackupNotRunning) {
		t.Fatalf("second finish = %v", err)
	}
	record, err := store.Backup(ctx, "backup")
	if err != nil || record.Status != "succeeded" || record.SizeBytes == nil || *record.SizeBytes != 123 || record.FinishedAtMillis == nil {
		t.Fatalf("backup record = %+v, %v", record, err)
	}
	if dirty != 0 {
		t.Fatalf("backup progress marked control dirty %d times", dirty)
	}
}
