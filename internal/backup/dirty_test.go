package backup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
