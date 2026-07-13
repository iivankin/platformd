package backup

import (
	"sync"
	"time"
)

type DirtyTracker struct {
	mutex sync.Mutex
	dirty bool
	since time.Time
	wake  chan struct{}
}

func NewDirtyTracker() *DirtyTracker {
	return &DirtyTracker{wake: make(chan struct{}, 1)}
}

func (tracker *DirtyTracker) Mark(at time.Time) {
	if at.IsZero() {
		return
	}
	tracker.mutex.Lock()
	if !tracker.dirty {
		tracker.dirty = true
		tracker.since = at
	}
	tracker.mutex.Unlock()
	tracker.signal()
}

func (tracker *DirtyTracker) Take() (time.Time, bool) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if !tracker.dirty {
		return time.Time{}, false
	}
	since := tracker.since
	tracker.dirty = false
	tracker.since = time.Time{}
	return since, true
}

func (tracker *DirtyTracker) Peek() (time.Time, bool) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	return tracker.since, tracker.dirty
}

func (tracker *DirtyTracker) Retry(since time.Time) {
	if since.IsZero() {
		return
	}
	tracker.mutex.Lock()
	if !tracker.dirty {
		tracker.dirty = true
		tracker.since = since
	}
	tracker.mutex.Unlock()
	tracker.signal()
}

func (tracker *DirtyTracker) Wake() <-chan struct{} { return tracker.wake }

func (tracker *DirtyTracker) signal() {
	select {
	case tracker.wake <- struct{}{}:
	default:
	}
}
