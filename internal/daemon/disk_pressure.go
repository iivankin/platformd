package daemon

import (
	"context"
	"time"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/systemevent"
)

type diskPressureAuditSink struct {
	store          *state.Store
	installationID string
}

func (sink diskPressureAuditSink) DiskPressureTransition(ctx context.Context, from, to diskpressure.Level, usage diskpressure.Usage, timestamp time.Time) error {
	auditID, err := id.New()
	if err != nil {
		return err
	}
	if err := sink.store.AppendDiskPressureAudit(ctx, state.DiskPressureAuditInput{
		ID: auditID, InstallationID: sink.installationID, From: from, To: to,
		Usage: usage, CreatedAtMillis: timestamp.UnixMilli(),
	}); err != nil {
		return err
	}
	systemevent.Warning(
		"disk_pressure_transition",
		systemevent.String("from", string(from)),
		systemevent.String("to", string(to)),
		systemevent.Uint64("available_bytes", usage.AvailableBytes),
		systemevent.Uint64("available_inodes", usage.AvailableInodes),
		systemevent.Uint64("byte_basis_points", usage.ByteBasisPoints),
		systemevent.Uint64("inode_basis_points", usage.InodeBasisPoints),
	)
	return nil
}
