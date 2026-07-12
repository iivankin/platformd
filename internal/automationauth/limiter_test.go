package automationauth

import (
	"fmt"
	"testing"
	"time"
)

func TestFailureLimiterEnforcesPairAndSourceWindows(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := newInMemoryFailureLimiter(func() time.Time { return now })
	for range pairFailureLimit {
		if allowed, _ := limiter.Permit("token-a", "192.0.2.1"); !allowed {
			t.Fatal("pair was limited before ten failures")
		}
		limiter.Failed("token-a", "192.0.2.1")
	}
	if allowed, retry := limiter.Permit("token-a", "192.0.2.1"); allowed || retry != time.Minute {
		t.Fatalf("pair limit = allowed %t, retry %v", allowed, retry)
	}
	if allowed, _ := limiter.Permit("token-b", "192.0.2.1"); !allowed {
		t.Fatal("pair limit affected another public ID before source limit")
	}

	for index := pairFailureLimit; index < sourceFailureLimit; index++ {
		limiter.Failed(fmt.Sprintf("token-%d", index), "198.51.100.9")
	}
	for range pairFailureLimit {
		limiter.Failed("token-base", "198.51.100.9")
	}
	if allowed, retry := limiter.Permit("fresh-token", "198.51.100.9"); allowed || retry != time.Minute {
		t.Fatalf("source limit = allowed %t, retry %v", allowed, retry)
	}

	now = now.Add(time.Minute)
	if allowed, retry := limiter.Permit("token-a", "192.0.2.1"); !allowed || retry != 0 {
		t.Fatalf("expired pair limit = allowed %t, retry %v", allowed, retry)
	}
	if len(limiter.pairs) != 0 || len(limiter.sources) != 0 {
		t.Fatalf("expired limiter state survived sweep: pairs=%d sources=%d", len(limiter.pairs), len(limiter.sources))
	}
}

func TestFailureLimiterSuccessDoesNotClearFailures(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := newInMemoryFailureLimiter(func() time.Time { return now })
	for range pairFailureLimit {
		limiter.Failed("token", "192.0.2.1")
	}
	limiter.Succeeded("token", "192.0.2.1")
	if allowed, _ := limiter.Permit("token", "192.0.2.1"); allowed {
		t.Fatal("successful authentication cleared failure window")
	}
}

func TestFailureLimiterCountsMalformedCredentialsOnlyAgainstSource(t *testing.T) {
	limiter := newInMemoryFailureLimiter(func() time.Time { return time.Unix(100, 0) })
	for range pairFailureLimit {
		limiter.Failed("", "192.0.2.1")
	}
	if allowed, _ := limiter.Permit("", "192.0.2.1"); !allowed {
		t.Fatal("malformed credential incorrectly consumed the per-ID limit")
	}
	if len(limiter.pairs) != 0 {
		t.Fatalf("malformed credentials created %d empty-ID pair entries", len(limiter.pairs))
	}
}
