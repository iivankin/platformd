package daemon

import (
	"context"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/containerlogs"
)

const (
	containerLogRetention       = 7 * 24 * time.Hour
	containerLogCleanupInterval = time.Hour
	containerLogBudgetBytes     = 5 << 30
)

func runContainerLogCleanup(
	ctx context.Context,
	cleaner *containerlogs.Cleaner,
	activeBasePaths func() map[string]struct{},
) {
	ticker := time.NewTicker(containerLogCleanupInterval)
	defer ticker.Stop()
	for {
		result, err := cleaner.Sweep(ctx, time.Now(), activeBasePaths())
		if err != nil && ctx.Err() == nil {
			log.Printf("container log cleanup: %v", err)
		}
		if result.BudgetExceeded {
			log.Printf("container log cleanup left %d bytes above its protected-file budget", result.RemainingBytes)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
