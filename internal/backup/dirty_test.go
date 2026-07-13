package backup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestDirtyTrackerKeepsFirstTimestampAndDoesNotLoseConcurrentMutation(t *testing.T) {
	t.Parallel()
	tracker := NewDirtyTracker()
	first := time.Unix(1, 0)
	second := time.Unix(2, 0)
	tracker.Mark(first)
	tracker.Mark(second)
	since, exists := tracker.Take()
	if !exists || !since.Equal(first) {
		t.Fatalf("coalesced dirty state = %v, %v", since, exists)
	}
	tracker.Mark(second)
	tracker.Retry(first)
	since, exists = tracker.Take()
	if !exists || !since.Equal(second) {
		t.Fatalf("concurrent mutation was overwritten by retry = %v, %v", since, exists)
	}
}

type controlRunnerStub struct {
	mutex   sync.Mutex
	calls   int
	results []error
	called  chan int
}

type resourceRunnerStub struct {
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (runner *resourceRunnerStub) RunResource(
	context.Context, string, string, *int64, int,
) (state.BackupRecord, error) {
	return state.BackupRecord{}, errors.New("unexpected scheduled resource backup")
}

func (runner *resourceRunnerStub) RunResourceStarted(
	_ context.Context,
	kind, resourceID string,
	_ *int64,
	_ int,
	onStarted func(state.BackupRecord),
) (state.BackupRecord, error) {
	record := state.BackupRecord{
		ID: "backup", ResourceKind: kind, ResourceID: resourceID,
		GenerationID: "generation", Status: "running", StartedAtMillis: 1,
	}
	onStarted(record)
	close(runner.started)
	<-runner.release
	record.Status = "succeeded"
	close(runner.done)
	return record, nil
}

func (runner *controlRunnerStub) RunControl(context.Context) error {
	runner.mutex.Lock()
	runner.calls++
	call := runner.calls
	var err error
	if call <= len(runner.results) {
		err = runner.results[call-1]
	}
	runner.mutex.Unlock()
	if runner.called != nil {
		runner.called <- call
	}
	return err
}

func TestWorkerRetriesFailureButNotMissingTargetOrPostPublishCleanup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		first     error
		wantCalls int
	}{
		{name: "failure", first: errors.New("remote failed"), wantCalls: 2},
		{name: "missing target", first: ErrControlTargetMissing, wantCalls: 1},
		{name: "published cleanup", first: &PublishedControlError{Err: errors.New("delete failed")}, wantCalls: 1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tracker := NewDirtyTracker()
			runner := &controlRunnerStub{results: []error{test.first}, called: make(chan int, 4)}
			worker, err := NewWorker(tracker, runner, 10*time.Millisecond, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			tracker.Mark(time.Unix(1, 0))
			for call := 1; call <= test.wantCalls; call++ {
				select {
				case got := <-runner.called:
					if got != call {
						t.Fatalf("worker call = %d, want %d", got, call)
					}
				case <-time.After(time.Second):
					t.Fatalf("worker did not make call %d", call)
				}
			}
			if test.wantCalls == 1 {
				select {
				case call := <-runner.called:
					t.Fatalf("non-retryable result made call %d", call)
				case <-time.After(30 * time.Millisecond):
				}
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("worker shutdown = %v", err)
			}
		})
	}
}

func TestWorkerAcceptsOnlyOneImmediateManualBackupWithoutQueue(t *testing.T) {
	t.Parallel()
	runner := &resourceRunnerStub{
		started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}),
	}
	worker, err := NewScheduledWorker(WorkerConfig{
		Dirty: NewDirtyTracker(), Control: &controlRunnerStub{},
		Store: scheduleStoreStub{}, Resources: runner,
		StartedAt: time.Unix(1, 0), Now: func() time.Time { return time.Unix(2, 0) },
		RetryDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(ctx) }()
	record, err := worker.TryRunNow(context.Background(), "redis", "redis-1", 7)
	if err != nil || record.Status != "running" || record.ID == "" {
		t.Fatalf("manual backup start = %+v, %v", record, err)
	}
	<-runner.started
	if _, err := worker.TryRunNow(context.Background(), "postgres", "postgres-1", 7); !errors.Is(err, ErrWorkerBusy) {
		t.Fatalf("second manual backup error = %v", err)
	}
	close(runner.release)
	<-runner.done
	cancel()
	if err := <-workerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("worker shutdown = %v", err)
	}
}
