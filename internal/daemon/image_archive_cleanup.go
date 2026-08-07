package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/state"
)

type imageArchiveStore interface {
	DeleteImageCleanupCandidates(context.Context, state.ImageCleanupMode, int64, int64) (state.ImageCleanupFiles, error)
}

type imageUploadCanceller interface {
	CancelExpired(context.Context, time.Time) error
	CancelAll(context.Context, time.Time) error
}

type imagePreviewStopper interface {
	stopAllPreviews(context.Context) error
}

type imageArchiveGarbageCollector struct {
	store    imageArchiveStore
	uploads  imageUploadCanceller
	previews imagePreviewStopper
	now      func() time.Time
}

func newImageArchiveGarbageCollector(
	store imageArchiveStore,
	uploads imageUploadCanceller,
	previews imagePreviewStopper,
) *imageArchiveGarbageCollector {
	return &imageArchiveGarbageCollector{store: store, uploads: uploads, previews: previews, now: time.Now}
}

func (collector *imageArchiveGarbageCollector) Cleanup(ctx context.Context, level diskpressure.Level) error {
	now := collector.now()
	if err := collector.uploads.CancelExpired(ctx, now); err != nil {
		return err
	}
	mode := state.ImageCleanupStandard
	if level == diskpressure.Critical || level == diskpressure.Emergency {
		if err := collector.uploads.CancelAll(ctx, now); err != nil {
			return err
		}
		if err := collector.previews.stopAllPreviews(ctx); err != nil {
			return err
		}
		mode = state.ImageCleanupCritical
		if level == diskpressure.Emergency {
			mode = state.ImageCleanupEmergency
		}
	}
	files, err := collector.store.DeleteImageCleanupCandidates(
		ctx, mode, now.UnixMilli(), now.Add(-inactiveFinalImageRetention).UnixMilli(),
	)
	if err != nil {
		return err
	}
	var failures []error
	for _, path := range append(files.UploadPaths, files.ArchivePaths...) {
		if removeErr := os.RemoveAll(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			failures = append(failures, removeErr)
		}
	}
	return errors.Join(failures...)
}
