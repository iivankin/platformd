//go:build linux && amd64 && cgo

package containerengine

import (
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/systemevent"
)

const imageStorageLockWarningAfter = 5 * time.Second

func (e *Engine) acquireImageReadLock(operation, target string) func() {
	startedAt := time.Now()
	waitDone := make(chan struct{})
	go func() {
		timer := time.NewTimer(imageStorageLockWarningAfter)
		defer timer.Stop()
		select {
		case <-waitDone:
		case <-timer.C:
			systemevent.Warning(
				"image_storage_lock_waiting",
				systemevent.String("operation", operation),
				systemevent.String("target", target),
				systemevent.Int64("elapsed_ms", imageStorageLockWarningAfter.Milliseconds()),
			)
		}
	}()
	e.imageOperations.RLock()
	close(waitDone)
	waited := time.Since(startedAt)
	if waited >= imageStorageLockWarningAfter {
		systemevent.Info(
			"image_storage_lock_acquired",
			systemevent.String("operation", operation),
			systemevent.String("target", target),
			systemevent.Int64("duration_ms", waited.Milliseconds()),
		)
	}
	var once sync.Once
	return func() {
		once.Do(e.imageOperations.RUnlock)
	}
}
