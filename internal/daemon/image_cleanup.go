package daemon

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
)

const inactiveFinalImageRetention = 14 * 24 * time.Hour
const buildCacheRetention = 24 * time.Hour
const imageCacheCleanupInterval = 24 * time.Hour
const diskPressureImageCleanupInterval = 5 * time.Minute

type imageCacheReferences interface {
	ReferencedContainerImageDigests(context.Context) (map[string]struct{}, error)
	KnownContainerImageDigests(context.Context) (map[string]struct{}, error)
}

type imageCacheCleaner interface {
	GarbageCollectImages(context.Context, containerengine.ImageGarbageCollectRequest) (containerengine.ImageGarbageCollectResult, error)
}

type imageGarbageCollector struct {
	references          imageCacheReferences
	cleaner             imageCacheCleaner
	now                 func() time.Time
	cleanupMu           sync.Mutex
	pressureMu          sync.Mutex
	lastPressureCleanup time.Time
}

func newImageGarbageCollector(references imageCacheReferences, cleaner imageCacheCleaner) *imageGarbageCollector {
	return &imageGarbageCollector{references: references, cleaner: cleaner, now: time.Now}
}

func runImageCacheCleanup(ctx context.Context, collector *imageGarbageCollector) {
	ticker := time.NewTicker(imageCacheCleanupInterval)
	defer ticker.Stop()
	for {
		collector.cleanupAndLog(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (collector *imageGarbageCollector) ForceGarbageCollect(ctx context.Context) (containerengine.ImageGarbageCollectResult, error) {
	result, err := collector.cleanup(ctx, true)
	logImageGarbageCollection(result, err)
	return result, err
}

func (collector *imageGarbageCollector) CleanupDiskPressure(ctx context.Context) error {
	now := collector.now()
	collector.pressureMu.Lock()
	if !collector.lastPressureCleanup.IsZero() && now.Sub(collector.lastPressureCleanup) < diskPressureImageCleanupInterval {
		collector.pressureMu.Unlock()
		return nil
	}
	collector.lastPressureCleanup = now
	collector.pressureMu.Unlock()
	result, err := collector.cleanup(ctx, true)
	if err != nil {
		collector.pressureMu.Lock()
		collector.lastPressureCleanup = time.Time{}
		collector.pressureMu.Unlock()
	}
	if ctx.Err() == nil {
		logImageGarbageCollection(result, err)
	}
	return err
}

func (collector *imageGarbageCollector) cleanupAndLog(ctx context.Context, aggressiveBuildCache bool) {
	result, err := collector.cleanup(ctx, aggressiveBuildCache)
	if ctx.Err() == nil {
		logImageGarbageCollection(result, err)
	}
}

func (collector *imageGarbageCollector) cleanup(
	ctx context.Context,
	aggressiveBuildCache bool,
) (containerengine.ImageGarbageCollectResult, error) {
	collector.cleanupMu.Lock()
	defer collector.cleanupMu.Unlock()
	protected, err := collector.references.ReferencedContainerImageDigests(ctx)
	if err != nil {
		return containerengine.ImageGarbageCollectResult{}, err
	}
	final, err := collector.references.KnownContainerImageDigests(ctx)
	if err != nil {
		return containerengine.ImageGarbageCollectResult{}, err
	}
	for digest := range protected {
		final[digest] = struct{}{}
	}
	now := collector.now()
	buildBefore := now.Add(-buildCacheRetention)
	if aggressiveBuildCache {
		buildBefore = now
	}
	return collector.cleaner.GarbageCollectImages(ctx, containerengine.ImageGarbageCollectRequest{
		FinalImageBefore:       now.Add(-inactiveFinalImageRetention),
		BuildCacheBefore:       buildBefore,
		OrphanLayerMaximumAge:  0,
		ProtectedDigests:       protected,
		KnownFinalImageDigests: final,
	})
}

func logImageGarbageCollection(result containerengine.ImageGarbageCollectResult, err error) {
	removed := result.FinalImagesRemoved + result.BuildCacheImagesRemoved + result.OrphanLayersRemoved
	if removed > 0 {
		log.Printf(
			"container image cleanup: final=%d build_cache=%d orphan_layers=%d bytes=%d skipped=%d",
			result.FinalImagesRemoved,
			result.BuildCacheImagesRemoved,
			result.OrphanLayersRemoved,
			result.RemovedBytes,
			result.Skipped,
		)
	}
	if err != nil {
		log.Printf("container image cleanup: %v", err)
	}
}
