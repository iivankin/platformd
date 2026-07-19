package daemon

import (
	"context"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/admission"
)

const objectStoreMultipartCleanupInterval = 15 * time.Minute
const objectStoreMultipartCleanupBatch = 250

type objectStoreMultipartCleaner interface {
	CleanupExpiredMultipart(context.Context, int) (int, error)
}

func startObjectStoreMultipartCleanup(ctx context.Context, cleaner objectStoreMultipartCleaner, gate *admission.Gate) {
	go func() {
		cleanupExpiredObjectStoreMultipart(ctx, cleaner, gate)
		ticker := time.NewTicker(objectStoreMultipartCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupExpiredObjectStoreMultipart(ctx, cleaner, gate)
			}
		}
	}()
}

func cleanupExpiredObjectStoreMultipart(ctx context.Context, cleaner objectStoreMultipartCleaner, gate *admission.Gate) {
	lease, err := gate.Begin("object_store_cleanup", "expired_multipart")
	if err != nil {
		return
	}
	defer lease.Release()
	cleaned, err := cleaner.CleanupExpiredMultipart(ctx, objectStoreMultipartCleanupBatch)
	if cleaned > 0 {
		log.Printf("object store multipart cleanup: removed=%d", cleaned)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("object store multipart cleanup: %v", err)
	}
}
