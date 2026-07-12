package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/state"
)

const (
	serviceLogSegmentBytes = 10 << 20
	serviceLogMaxFiles     = 3
)

func (stack *runtimeStack) ConfigureDeployments(ctx context.Context, store *state.Store, credentials deployment.CredentialResolver) error {
	controller, err := deployment.New(deployment.Config{
		Store: store, Engine: stack.engine, Publisher: stack, Credentials: credentials,
		Placement: stack.servicePlacement,
		LogRoot:   stack.paths.LogsRoot, VolumeRoot: stack.paths.VolumesRoot,
		LogSizeBytes: serviceLogSegmentBytes, LogMaxFiles: serviceLogMaxFiles,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.deployments = controller
	stack.mu.Unlock()

	serviceIDs, err := store.EnabledServiceIDs(ctx)
	if err != nil {
		return err
	}
	for _, serviceID := range serviceIDs {
		desired, loadErr := store.DesiredService(ctx, serviceID)
		if loadErr != nil {
			stack.recordServiceFailure(serviceID, loadErr)
			continue
		}
		if desired.ActiveDeploymentID != "" {
			if restoreErr := controller.Restore(ctx, serviceID); restoreErr != nil {
				stack.recordServiceFailure(serviceID, restoreErr)
				continue
			}
		}
		if deployErr := controller.Deploy(ctx, serviceID, false); deployErr != nil && !errors.Is(deployErr, deployment.ErrBlockedPair) {
			stack.recordServiceFailure(serviceID, deployErr)
		}
	}
	return nil
}

func (stack *runtimeStack) DeployService(ctx context.Context, serviceID string, force bool) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed {
		return errors.New("container runtime is closed")
	}
	if controller == nil {
		return errors.New("deployment controller is not configured")
	}
	err := controller.Deploy(ctx, serviceID, force)
	stack.mu.Lock()
	if err == nil {
		delete(stack.serviceFailures, serviceID)
	} else {
		stack.serviceFailures[serviceID] = err
	}
	stack.mu.Unlock()
	return err
}

func (stack *runtimeStack) servicePlacement(service state.ServiceDesired) (deployment.Placement, error) {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.closed {
		return deployment.Placement{}, errors.New("container runtime is closed")
	}
	network, ok := stack.projectNetworks[service.ProjectID]
	if !ok {
		return deployment.Placement{}, fmt.Errorf("project %s has no runtime network", service.ProjectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return deployment.Placement{}, fmt.Errorf("parse project gateway: %w", err)
	}
	return deployment.Placement{
		NetworkName: network.Name, Gateway: gateway,
		DNSSearch:    service.ProjectName + ".internal",
		CgroupParent: path.Join(stack.cgroupRoot, "service-"+service.ID),
	}, nil
}

func (stack *runtimeStack) Publish(service state.ServiceDesired, container containerengine.Container) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[service.ProjectID]
	network, ok := stack.projectNetworks[service.ProjectID]
	if zone == nil || !ok {
		return fmt.Errorf("project %s DNS runtime is unavailable", service.ProjectID)
	}
	addresses := container.IPs[network.Name]
	if len(addresses) != 1 {
		return fmt.Errorf("service container has %d project addresses, want one", len(addresses))
	}
	address, err := netip.ParseAddr(addresses[0])
	if err != nil {
		return fmt.Errorf("parse service address: %w", err)
	}
	return zone.Set(service.Name+"."+service.ProjectName+".internal", address)
}

func (stack *runtimeStack) Withdraw(service state.ServiceDesired) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	zone := stack.dnsZones[service.ProjectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", service.ProjectID)
	}
	return zone.Delete(service.Name + "." + service.ProjectName + ".internal")
}

func (stack *runtimeStack) recordServiceFailure(serviceID string, err error) {
	stack.mu.Lock()
	stack.serviceFailures[serviceID] = err
	stack.mu.Unlock()
}
