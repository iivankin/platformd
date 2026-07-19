package daemon

import (
	"context"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
)

const imageCacheRetention = 14 * 24 * time.Hour
const imageCacheCleanupInterval = 24 * time.Hour

type imageCacheReferences interface {
	ReferencedContainerImageDigests(context.Context) (map[string]struct{}, error)
}

type imageCacheCleaner interface {
	GarbageCollectImages(context.Context, containerengine.ImageGarbageCollectRequest) (containerengine.ImageGarbageCollectResult, error)
}

func runImageCacheCleanup(ctx context.Context, references imageCacheReferences, cleaner imageCacheCleaner) {
	ticker := time.NewTicker(imageCacheCleanupInterval)
	defer ticker.Stop()
	for {
		cleanupImageCache(ctx, references, cleaner, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func cleanupImageCache(ctx context.Context, references imageCacheReferences, cleaner imageCacheCleaner, now time.Time) {
	digests, err := references.ReferencedContainerImageDigests(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("container image cleanup: %v", err)
		}
		return
	}
	result, err := cleaner.GarbageCollectImages(ctx, containerengine.ImageGarbageCollectRequest{
		Before: now.Add(-imageCacheRetention), ProtectedDigests: digests,
	})
	if result.Removed > 0 {
		log.Printf("container image cleanup: removed=%d bytes=%d skipped=%d", result.Removed, result.RemovedBytes, result.Skipped)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("container image cleanup: %v", err)
	}
}
