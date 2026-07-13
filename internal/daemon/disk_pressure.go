package daemon

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type diskPressureAuditSink struct {
	store          *state.Store
	installationID string
}

func (sink diskPressureAuditSink) DiskPressureTransition(ctx context.Context, from, to diskpressure.Level, usage diskpressure.Usage, timestamp time.Time) error {
	auditID, err := id.NewWith(timestamp, rand.Reader)
	if err != nil {
		return err
	}
	return sink.store.AppendDiskPressureAudit(ctx, state.DiskPressureAuditInput{
		ID: auditID, InstallationID: sink.installationID, From: from, To: to,
		Usage: usage, CreatedAtMillis: timestamp.UnixMilli(),
	})
}
