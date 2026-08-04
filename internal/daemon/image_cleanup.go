package daemon

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/diskpressure"
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
	lastPressureLevel   diskpressure.Level
	archiveMu           sync.RWMutex
	archive             *imageArchiveGarbageCollector
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
	var err error
	if archive := collector.archiveCollector(); archive != nil {
		err = archive.Cleanup(ctx, diskpressure.Low)
	}
	result, cacheErr := collector.cleanup(ctx, true)
	err = errors.Join(err, cacheErr)
	logImageGarbageCollection(result, err)
	return result, err
}

func (collector *imageGarbageCollector) CleanupDiskPressure(ctx context.Context, level diskpressure.Level) error {
	now := collector.now()
	collector.pressureMu.Lock()
	if !collector.lastPressureCleanup.IsZero() && now.Sub(collector.lastPressureCleanup) < diskPressureImageCleanupInterval &&
		imageCleanupPressureRank(level) <= imageCleanupPressureRank(collector.lastPressureLevel) {
		collector.pressureMu.Unlock()
		return nil
	}
	collector.lastPressureCleanup = now
	collector.lastPressureLevel = level
	collector.pressureMu.Unlock()
	var err error
	if archive := collector.archiveCollector(); archive != nil {
		err = archive.Cleanup(ctx, level)
	}
	result, cacheErr := collector.cleanup(ctx, true)
	err = errors.Join(err, cacheErr)
	if err != nil {
		collector.pressureMu.Lock()
		collector.lastPressureCleanup = time.Time{}
		collector.lastPressureLevel = ""
		collector.pressureMu.Unlock()
	}
	if ctx.Err() == nil {
		logImageGarbageCollection(result, err)
	}
	return err
}

func (collector *imageGarbageCollector) cleanupAndLog(ctx context.Context, aggressiveBuildCache bool) {
	var err error
	if archive := collector.archiveCollector(); archive != nil {
		err = archive.Cleanup(ctx, diskpressure.Low)
	}
	result, cacheErr := collector.cleanup(ctx, aggressiveBuildCache)
	err = errors.Join(err, cacheErr)
	if ctx.Err() == nil {
		logImageGarbageCollection(result, err)
	}
}

func imageCleanupPressureRank(level diskpressure.Level) int {
	switch level {
	case diskpressure.Emergency:
		return 3
	case diskpressure.Critical:
		return 2
	case diskpressure.Low:
		return 1
	default:
		return 0
	}
}

func (collector *imageGarbageCollector) setArchiveCollector(archive *imageArchiveGarbageCollector) {
	collector.archiveMu.Lock()
	collector.archive = archive
	collector.archiveMu.Unlock()
}

func (collector *imageGarbageCollector) archiveCollector() *imageArchiveGarbageCollector {
	collector.archiveMu.RLock()
	defer collector.archiveMu.RUnlock()
	return collector.archive
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
