package daemon

import (
	"context"
	"testing"
	"time"
)

type auditCleanerStub struct {
	before int64
	limit  int
}

func (cleaner *auditCleanerStub) CleanupAuditEvents(_ context.Context, before int64, limit int) (int64, error) {
	cleaner.before = before
	cleaner.limit = limit
	return 1, nil
}

func TestAuditCleanupUsesSevenDayCutoffAndBoundedBatch(t *testing.T) {
	cleaner := &auditCleanerStub{}
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	cleanupAuditBatch(context.Background(), cleaner, now)
	if cleaner.before != now.Add(-7*24*time.Hour).UnixMilli() || cleaner.limit != 500 {
		t.Fatalf("cleanup cutoff/limit = %d/%d", cleaner.before, cleaner.limit)
	}
}
