package automationauth

import (
	"sync"
	"time"
)

const (
	failureWindow      = time.Minute
	retryAfter         = time.Minute
	pairFailureLimit   = 10
	sourceFailureLimit = 100
)

type failureKey struct {
	publicID string
	source   string
}

type failureWindowState struct {
	startedAt time.Time
	count     int
}

type InMemoryFailureLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	pairs     map[failureKey]failureWindowState
	sources   map[string]failureWindowState
	lastSweep time.Time
}

func NewInMemoryFailureLimiter() *InMemoryFailureLimiter {
	return newInMemoryFailureLimiter(time.Now)
}

func newInMemoryFailureLimiter(now func() time.Time) *InMemoryFailureLimiter {
	return &InMemoryFailureLimiter{
		now: now, pairs: make(map[failureKey]failureWindowState),
		sources: make(map[string]failureWindowState), lastSweep: now(),
	}
}

func (limiter *InMemoryFailureLimiter) Permit(publicID, source string) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	limiter.sweep(now)
	sourceState := currentFailureWindow(limiter.sources[source], now)
	pairLimited := false
	if publicID != "" {
		pair := currentFailureWindow(limiter.pairs[failureKey{publicID: publicID, source: source}], now)
		pairLimited = pair.count >= pairFailureLimit
	}
	if pairLimited || sourceState.count >= sourceFailureLimit {
		return false, retryAfter
	}
	return true, 0
}

func (limiter *InMemoryFailureLimiter) Failed(publicID, source string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	if publicID != "" {
		key := failureKey{publicID: publicID, source: source}
		pair := currentFailureWindow(limiter.pairs[key], now)
		pair.count++
		limiter.pairs[key] = pair
	}
	sourceState := currentFailureWindow(limiter.sources[source], now)
	sourceState.count++
	limiter.sources[source] = sourceState
}

func (*InMemoryFailureLimiter) Succeeded(string, string) {
	// Successful requests intentionally do not reset failure counters. This
	// keeps a leaked valid token from masking concurrent guessing from one source.
}

func currentFailureWindow(state failureWindowState, now time.Time) failureWindowState {
	if state.startedAt.IsZero() || !now.Before(state.startedAt.Add(failureWindow)) {
		return failureWindowState{startedAt: now}
	}
	return state
}

func (limiter *InMemoryFailureLimiter) sweep(now time.Time) {
	if now.Before(limiter.lastSweep.Add(failureWindow)) {
		return
	}
	for key, state := range limiter.pairs {
		if !now.Before(state.startedAt.Add(failureWindow)) {
			delete(limiter.pairs, key)
		}
	}
	for source, state := range limiter.sources {
		if !now.Before(state.startedAt.Add(failureWindow)) {
			delete(limiter.sources, source)
		}
	}
	limiter.lastSweep = now
}
