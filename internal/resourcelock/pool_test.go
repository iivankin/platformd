package resourcelock

import (
	"sync"
	"testing"
)

func TestPoolRetainsWaitedLockAndEvictsItWhenIdle(t *testing.T) {
	t.Parallel()
	var pool Pool
	first := pool.Get("resource")
	first.Lock()
	second := pool.Get("resource")
	if first != second {
		t.Fatal("concurrent users received different resource locks")
	}

	acquired := make(chan struct{})
	var waiter sync.WaitGroup
	waiter.Add(1)
	go func() {
		defer waiter.Done()
		second.Lock()
		close(acquired)
		second.Unlock()
	}()
	first.Unlock()
	<-acquired
	waiter.Wait()

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.locks) != 0 {
		t.Fatalf("idle resource locks = %d, want 0", len(pool.locks))
	}
}
