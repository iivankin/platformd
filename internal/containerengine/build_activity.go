package containerengine

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/iivankin/platformd/internal/systemevent"
)

const buildStallWarningAfter = 30 * time.Second

type buildActivityWriter struct {
	destination io.Writer
	lastOutput  atomic.Int64
}

func newBuildActivityWriter(destination io.Writer, startedAt time.Time) *buildActivityWriter {
	writer := &buildActivityWriter{destination: destination}
	writer.lastOutput.Store(startedAt.UnixNano())
	return writer
}

func (writer *buildActivityWriter) Write(value []byte) (int, error) {
	writer.lastOutput.Store(time.Now().UnixNano())
	return writer.destination.Write(value)
}

func (writer *buildActivityWriter) watch(done <-chan struct{}, reference string, startedAt time.Time) {
	ticker := time.NewTicker(buildStallWarningAfter / 3)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			lastOutput := time.Unix(0, writer.lastOutput.Load())
			if now.Sub(startedAt) < buildStallWarningAfter || now.Sub(lastOutput) < buildStallWarningAfter {
				continue
			}
			systemevent.Warning(
				"image_build_no_output",
				systemevent.String("reference", reference),
				systemevent.Int64("elapsed_ms", now.Sub(startedAt).Milliseconds()),
				systemevent.Int64("silent_ms", now.Sub(lastOutput).Milliseconds()),
			)
			return
		}
	}
}
