package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
)

type imageCacheReferencesStub struct{}

func (imageCacheReferencesStub) ReferencedContainerImageDigests(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{"sha256:active": {}}, nil
}

func (imageCacheReferencesStub) KnownContainerImageDigests(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{"sha256:inactive": {}}, nil
}

type imageCacheCleanerStub struct {
	requests []containerengine.ImageGarbageCollectRequest
}

func (cleaner *imageCacheCleanerStub) GarbageCollectImages(
	_ context.Context,
	request containerengine.ImageGarbageCollectRequest,
) (containerengine.ImageGarbageCollectResult, error) {
	cleaner.requests = append(cleaner.requests, request)
	return containerengine.ImageGarbageCollectResult{}, nil
}

func TestImageGarbageCollectorSeparatesScheduledAndForcedRetention(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	cleaner := &imageCacheCleanerStub{}
	collector := newImageGarbageCollector(imageCacheReferencesStub{}, cleaner)
	collector.now = func() time.Time { return now }

	if _, err := collector.cleanup(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.cleanup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(cleaner.requests) != 2 {
		t.Fatalf("cleanup requests = %d", len(cleaner.requests))
	}
	scheduled, forced := cleaner.requests[0], cleaner.requests[1]
	if !scheduled.FinalImageBefore.Equal(now.Add(-inactiveFinalImageRetention)) ||
		!scheduled.BuildCacheBefore.Equal(now.Add(-buildCacheRetention)) {
		t.Fatalf("scheduled cutoffs = %+v", scheduled)
	}
	if !forced.FinalImageBefore.Equal(scheduled.FinalImageBefore) || !forced.BuildCacheBefore.Equal(now) {
		t.Fatalf("forced cutoffs = %+v", forced)
	}
	for _, request := range cleaner.requests {
		if _, ok := request.ProtectedDigests["sha256:active"]; !ok {
			t.Fatal("active image digest is not protected")
		}
		if _, ok := request.KnownFinalImageDigests["sha256:inactive"]; !ok {
			t.Fatal("inactive final image digest is not classified as final")
		}
		if _, ok := request.KnownFinalImageDigests["sha256:active"]; !ok {
			t.Fatal("active image digest is not classified as final")
		}
	}
}

func TestDiskPressureImageCleanupHasCooldown(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	cleaner := &imageCacheCleanerStub{}
	collector := newImageGarbageCollector(imageCacheReferencesStub{}, cleaner)
	collector.now = func() time.Time { return now }

	if err := collector.CleanupDiskPressure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := collector.CleanupDiskPressure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cleaner.requests) != 1 {
		t.Fatalf("cleanup requests during cooldown = %d", len(cleaner.requests))
	}
	now = now.Add(diskPressureImageCleanupInterval)
	if err := collector.CleanupDiskPressure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cleaner.requests) != 2 {
		t.Fatalf("cleanup requests after cooldown = %d", len(cleaner.requests))
	}
}
