package admission

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestUpdateAdmissionIsAtomicAndObservational(t *testing.T) {
	t.Parallel()
	gate := New()
	backup, err := gate.Begin("backup", "backup-b")
	if err != nil {
		t.Fatal(err)
	}
	console, err := gate.Begin("container_console", "console-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, snapshot, err := gate.TryUpdate(); !errors.Is(err, ErrBusy) || snapshot.Total != 2 || snapshot.Truncated ||
		snapshot.Blockers[0] != (Blocker{Kind: "backup", ID: "backup-b"}) {
		t.Fatalf("busy update = %+v, %v", snapshot, err)
	}
	backup.Release()
	console.Release()

	update, snapshot, err := gate.TryUpdate()
	if err != nil || snapshot.Total != 0 {
		t.Fatalf("idle update = %+v, %v", snapshot, err)
	}
	if _, err := gate.Begin("deploy", "deployment"); !errors.Is(err, ErrUpdating) {
		t.Fatalf("mutation during update = %v", err)
	}
	if _, _, err := gate.TryUpdate(); !errors.Is(err, ErrUpdating) {
		t.Fatalf("second update = %v", err)
	}
	update.Release()
	update.Release()
	if lease, err := gate.Begin("deploy", "deployment"); err != nil {
		t.Fatal(err)
	} else {
		lease.Release()
	}
}

func TestBlockerSnapshotIsBoundedAndRaceSafe(t *testing.T) {
	t.Parallel()
	gate := New()
	var wait sync.WaitGroup
	for index := 0; index < maximumBlockers+10; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			lease, err := gate.Begin("sql", fmt.Sprintf("query-%03d", index))
			if err != nil {
				t.Errorf("begin blocker: %v", err)
				return
			}
			t.Cleanup(lease.Release)
		}(index)
	}
	wait.Wait()
	snapshot, updating := gate.Snapshot()
	if updating || snapshot.Total != maximumBlockers+10 || len(snapshot.Blockers) != maximumBlockers || !snapshot.Truncated {
		t.Fatalf("bounded snapshot = %+v, updating=%t", snapshot, updating)
	}
}
