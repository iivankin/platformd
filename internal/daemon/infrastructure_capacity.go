package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/diskusage"
)

type infrastructureCapacity struct {
	pressure   *diskpressure.Manager
	components *diskusage.Scanner
}

func (capacity infrastructureCapacity) Snapshot() (diskpressure.Snapshot, bool) {
	return capacity.pressure.Snapshot()
}

func (capacity infrastructureCapacity) Components(ctx context.Context) (diskusage.Snapshot, error) {
	return capacity.components.Components(ctx)
}
