package daemon

import (
	"context"
	"sync/atomic"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/diskusage"
	"github.com/iivankin/platformd/internal/telemetry"
)

type infrastructureCapacity struct {
	pressure   *diskpressure.Manager
	components *diskusage.Scanner
}

func (capacity infrastructureCapacity) Snapshot() (diskpressure.Snapshot, bool) {
	return capacity.pressure.Snapshot()
}

func (capacity infrastructureCapacity) Components(ctx context.Context) (diskusage.Snapshot, error) {
	capacity.components.Invalidate("recordings")
	return capacity.components.Components(ctx)
}

type telemetryRecordingUsage struct {
	process atomic.Pointer[telemetry.Process]
}

func (usage *telemetryRecordingUsage) Set(process *telemetry.Process) {
	usage.process.Store(process)
}

func (usage *telemetryRecordingUsage) Bytes(ctx context.Context) (uint64, error) {
	process := usage.process.Load()
	if process == nil {
		return 0, nil
	}
	return process.RecordingBytes(ctx)
}
