package daemon

import (
	"context"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const auditRetention = 7 * 24 * time.Hour
const auditCleanupInterval = time.Minute
const auditCleanupBatch = 500

type auditCleaner interface {
	CleanupAuditEvents(context.Context, int64, int) (int64, error)
}

func startAuditCleanup(ctx context.Context, cleaner auditCleaner) {
	go func() {
		cleanupAuditBatch(ctx, cleaner, time.Now())
		ticker := time.NewTicker(auditCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case timestamp := <-ticker.C:
				cleanupAuditBatch(ctx, cleaner, timestamp)
			}
		}
	}()
}

func cleanupAuditBatch(ctx context.Context, cleaner auditCleaner, timestamp time.Time) {
	if _, err := cleaner.CleanupAuditEvents(ctx, timestamp.Add(-auditRetention).UnixMilli(), auditCleanupBatch); err != nil && ctx.Err() == nil {
		log.Printf("audit cleanup failed: %v", err)
	}
}

var _ auditCleaner = (*state.Store)(nil)
