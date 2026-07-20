package daemon

import (
	"context"
	"log"
	"time"

	"github.com/iivankin/platformd/internal/cloudflaremesh"
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
			log.Printf("repair managed Cloudflare Mesh sidecar: %v", err)
			delay = min(delay*2, cloudflareMeshMaximumBackoff)
			continue
		}
		delay = cloudflareMeshHealthInterval
		if !repaired {
			continue
		}
		if err := gateways.ReconcileMeshNetworkGateways(ctx); err != nil {
			log.Printf("rebind Cloudflare Mesh gateways: %v", err)
		}
	}
}
