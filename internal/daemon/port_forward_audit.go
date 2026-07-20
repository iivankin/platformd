package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/state"
)

type livePortForwardAudit struct{ store *state.Store }

func (audit livePortForwardAudit) RecordPortForwardTicket(ctx context.Context, record portforward.AuditRecord) error {
	return audit.store.RecordPortForwardTicket(ctx, state.RecordPortForwardTicket{
		AuditEventID: record.ID, ActorTokenID: record.ActorTokenID, TicketID: record.TicketID,
		ProjectID: record.ProjectID, ResourceKind: record.ResourceKind, ResourceID: record.ResourceID,
		Port: record.Port, CreatedAtMillis: record.CreatedAt.UnixMilli(), ExpiresAtMillis: record.ExpiresAt.UnixMilli(),
	})
}
