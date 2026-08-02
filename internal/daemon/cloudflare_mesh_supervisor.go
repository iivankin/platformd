package daemon

import (
	"context"
	"time"

	"github.com/iivankin/platformd/internal/cloudflaremesh"
	"github.com/iivankin/platformd/internal/systemevent"
)

const (
	cloudflareMeshHealthInterval = 15 * time.Second
	cloudflareMeshMaximumBackoff = 5 * time.Minute
)

func (stack *runtimeStack) startCloudflareMeshSupervisor(
	ctx context.Context,
	mesh *cloudflaremesh.Application,
	gateways *liveNetworkGatewayRepository,
) {
	supervisorContext, cancel := context.WithCancel(ctx)
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		cancel()
		return
	}
	stack.cloudflareMeshCancel = cancel
	stack.mu.Unlock()
	go superviseCloudflareMesh(supervisorContext, mesh, gateways)
}

func superviseCloudflareMesh(
	ctx context.Context,
	mesh *cloudflaremesh.Application,
	gateways *liveNetworkGatewayRepository,
) {
	delay := cloudflareMeshHealthInterval
	systemevent.Info("cloudflare_mesh_supervisor_started")
	defer systemevent.Info("cloudflare_mesh_supervisor_stopped")
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}

		repaired, err := mesh.RepairConnection(ctx)
		if err != nil {
			delay = min(delay*2, cloudflareMeshMaximumBackoff)
			systemevent.Failure(
				"cloudflare_mesh_repair_failed",
				err,
				systemevent.Int64("retry_ms", delay.Milliseconds()),
			)
			continue
		}
		delay = cloudflareMeshHealthInterval
		if !repaired {
			continue
		}
		systemevent.Info("cloudflare_mesh_repaired")
		if err := gateways.ReconcileMeshNetworkGateways(ctx); err != nil {
			systemevent.Failure("cloudflare_mesh_gateway_rebind_failed", err)
		} else {
			systemevent.Info("cloudflare_mesh_gateways_rebound")
		}
	}
}
