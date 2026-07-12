package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

func (stack *runtimeStack) ConfigureManagedRedis(ctx context.Context, store *state.Store, master cryptobox.MasterKey) error {
	controller, err := managedredis.NewController(managedredis.Config{
		Store: store, Engine: stack.engine, Publisher: stack,
		Password: func(resource state.ManagedRedis) (string, error) {
			return managedredis.OpenPassword(master, resource.ID, resource.PasswordEncrypted)
		},
		Placement:     stack.redisPlacement,
		GeneratedRoot: stack.paths.GeneratedRoot, VolumeRoot: stack.paths.VolumesRoot,
		LogRoot: stack.paths.LogsRoot, LogSizeBytes: serviceLogSegmentBytes,
		LogMaxFiles: serviceLogMaxFiles,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.managedRedis = controller
	stack.mu.Unlock()
	resources, err := store.ManagedRedisResources(ctx)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		if err := controller.Start(ctx, resource.ID); err != nil {
			stack.recordRedisFailure(resource.ID, err)
		}
	}
	return nil
}

func (stack *runtimeStack) redisPlacement(resource state.ManagedRedis) (managedredis.Placement, error) {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return managedredis.Placement{}, errors.New("container runtime is closed")
	}
	network, ok := stack.projectNetworks[resource.ProjectID]
	if !ok {
		return managedredis.Placement{}, fmt.Errorf("project %s has no runtime network", resource.ProjectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return managedredis.Placement{}, fmt.Errorf("parse project gateway: %w", err)
	}
	return managedredis.Placement{
		NetworkName: network.Name, Gateway: gateway,
		DNSSearch:    resource.ProjectName + ".internal",
		CgroupParent: path.Join(stack.cgroupRoot, "redis-"+resource.ID),
	}, nil
}

func (stack *runtimeStack) PublishRedis(resource state.ManagedRedis, container containerengine.Container) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[resource.ProjectID]
	network, ok := stack.projectNetworks[resource.ProjectID]
	if zone == nil || !ok {
		return fmt.Errorf("project %s DNS runtime is unavailable", resource.ProjectID)
	}
	addresses := container.IPs[network.Name]
	if len(addresses) != 1 {
		return fmt.Errorf("managed Redis container has %d project addresses, want one", len(addresses))
	}
	address, err := netip.ParseAddr(addresses[0])
	if err != nil {
		return fmt.Errorf("parse managed Redis address: %w", err)
	}
	if err := zone.Set(resource.Name+"."+resource.ProjectName+".internal", address); err != nil {
		return err
	}
	delete(stack.redisFailures, resource.ID)
	return nil
}

func (stack *runtimeStack) WithdrawRedis(resource state.ManagedRedis) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[resource.ProjectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", resource.ProjectID)
	}
	return zone.Delete(resource.Name + "." + resource.ProjectName + ".internal")
}

func (stack *runtimeStack) RedisStatus(resourceID string) (string, string) {
	stack.mu.Lock()
	controller := stack.managedRedis
	failure := stack.redisFailures[resourceID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		if failure != nil {
			return "failed", failure.Error()
		}
		return "pending", "Runtime is not ready"
	}
	container, active, err := controller.Status(resourceID)
	if err != nil {
		return "failed", err.Error()
	}
	if active && container.State == "running" {
		return "running", ""
	}
	if failure != nil {
		return "failed", failure.Error()
	}
	if active {
		return "failed", fmt.Sprintf("Container is %s (exit code %d)", container.State, container.ExitCode)
	}
	return "pending", "Waiting for the first successful start"
}

func (stack *runtimeStack) recordRedisFailure(resourceID string, err error) {
	stack.mu.Lock()
	stack.redisFailures[resourceID] = err
	stack.mu.Unlock()
}
