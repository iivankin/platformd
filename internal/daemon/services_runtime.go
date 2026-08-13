package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/preview"
	"github.com/iivankin/platformd/internal/projectwebhook"
	"github.com/iivankin/platformd/internal/servicerestart"
	"github.com/iivankin/platformd/internal/servicewatcher"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/systemevent"
)

const (
	serviceLogSegmentBytes = 10 << 20
	serviceLogMaxFiles     = 3
)

type publishedBackend struct {
	deploymentID string
	address      string
}

type serviceBackendSnapshot struct {
	// A snapshot is immutable after publication so proxy lookups need only one
	// atomic load and a map lookup.
	services map[string]publishedBackend
}

func (stack *runtimeStack) ConfigureDeployments(ctx context.Context, store *state.Store, master cryptobox.MasterKey, credentials deployment.CredentialResolver, cloudflareApplication *cloudflaredns.Application, webhooks *projectwebhook.Application, containerLogs deployment.ContainerLogSink) error {
	controller, err := deployment.New(deployment.Config{
		Store: store, Engine: stack.engine, Publisher: stack, Credentials: credentials,
		Environment: resourceVariableResolver{store: store, master: master},
		Growth:      stack.growth, Admission: stack.admission,
		Webhooks: webhooks,
		BeforeDeploy: beforeDeployExecutor{
			engine: stack.engine, environment: resourceVariableResolver{store: store, master: master},
			placement: stack.servicePlacement, cloudflare: cloudflareApplication,
			logSizeBytes: serviceLogSegmentBytes, logMaxFiles: serviceLogMaxFiles,
		},
		Placement: stack.servicePlacement,
		LogRoot:   stack.paths.LogsRoot, VolumeRoot: stack.paths.VolumesRoot,
		ContainerLogs: containerLogs,
	})
	if err != nil {
		return err
	}
	restarts, err := servicerestart.New(servicerestart.Config{
		Context: ctx, Engine: stack.engine, Controller: controller,
		OnResult: stack.recordServiceResult,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		restarts.Close()
		return errors.New("container runtime is closed")
	}
	stack.deployments = controller
	stack.serviceRestarts = restarts
	stack.mu.Unlock()
	return nil
}

func (stack *runtimeStack) ReconcileDeployments(ctx context.Context, store *state.Store) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not configured")
	}
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

func (stack *runtimeStack) ConfigureServiceWatcher(ctx context.Context, store *state.Store) error {
	watcher, err := servicewatcher.New(servicewatcher.Config{
		Store: store, Deployer: stack,
	})
	if err != nil {
		return err
	}
	if err := watcher.Start(ctx, stack.hasServiceFailure); err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.serviceWatcher = watcher
	stack.mu.Unlock()
	return nil
}

func (stack *runtimeStack) TrackService(ctx context.Context, serviceID string, retry bool) error {
	stack.mu.Lock()
	watcher := stack.serviceWatcher
	stack.mu.Unlock()
	if watcher == nil {
		return errors.New("service watcher is not configured")
	}
	return watcher.Track(ctx, serviceID, retry)
}

func (stack *runtimeStack) ReconcileService(ctx context.Context, serviceID string) error {
	stack.mu.Lock()
	watcher := stack.serviceWatcher
	stack.mu.Unlock()
	if watcher == nil {
		return errors.New("service watcher is not configured")
	}
	return watcher.Reconcile(ctx, serviceID)
}

func (stack *runtimeStack) hasServiceFailure(serviceID string) bool {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	err := stack.serviceFailures[serviceID]
	return err != nil &&
		!errors.Is(err, deployment.ErrImageUploadRequired) &&
		!errors.Is(err, state.ErrServiceChanged)
}

func (stack *runtimeStack) DeployService(ctx context.Context, serviceID string, force bool) error {
	return stack.deployService(ctx, serviceID, func(controller *deployment.Controller) error {
		return controller.Deploy(ctx, serviceID, force)
	})
}

func (stack *runtimeStack) DeployUploadedProduction(
	ctx context.Context,
	serviceID, deploymentID, imageRevisionID, imageReference string,
	image containerengine.Image,
	identity state.ImageUploadIdentity,
) error {
	return stack.deployService(ctx, serviceID, func(controller *deployment.Controller) error {
		return controller.DeployUploadedImage(ctx, serviceID, deploymentID, imageRevisionID, imageReference, image, identity)
	})
}

func (stack *runtimeStack) DeployServiceImage(ctx context.Context, serviceID string, previous state.DeploymentRecord) error {
	image, err := stack.engine.InspectImage(ctx, previous.ImageDigest)
	if err != nil {
		image, err = stack.engine.Pull(ctx, containerengine.PullRequest{Reference: previous.ImageReference})
	}
	if err != nil {
		return fmt.Errorf("load historical uploaded image: %w", err)
	}
	if image.Digest != previous.ImageDigest {
		return fmt.Errorf("historical uploaded image digest = %s, want %s", image.Digest, previous.ImageDigest)
	}
	deploymentID, err := id.New()
	if err != nil {
		return err
	}
	return stack.DeployUploadedProduction(ctx, serviceID, deploymentID, previous.ImageRevisionID, previous.ImageReference, image, state.ImageUploadIdentity{SHA: previous.SourceRevision})
}

func (stack *runtimeStack) deployService(ctx context.Context, serviceID string, deploy func(*deployment.Controller) error) error {
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
	err := deploy(controller)
	stack.recordServiceResult(serviceID, err)
	return err
}

func (stack *runtimeStack) RestartServiceDeployment(ctx context.Context, serviceID, deploymentID string) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	err := controller.RestartCurrent(ctx, serviceID, deploymentID)
	stack.recordServiceResult(serviceID, err)
	return err
}

func (stack *runtimeStack) DeleteServiceDeploymentLogs(serviceID, deploymentID string) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	return controller.DeleteDeploymentLogs(serviceID, deploymentID)
}

func (stack *runtimeStack) DeleteService(ctx context.Context, service state.ServiceDesired) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	err := controller.DeleteService(ctx, service)
	stack.recordServiceResult(service.ID, err)
	return err
}

func (stack *runtimeStack) deleteServiceDuringProjectDeletion(ctx context.Context, service state.ServiceDesired) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	return controller.DeleteServiceDuringProjectDeletion(ctx, service)
}

func (stack *runtimeStack) DeleteServiceLogs(serviceID string) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	return controller.DeleteServiceLogs(serviceID)
}

func (stack *runtimeStack) WithServiceQuiesced(ctx context.Context, serviceID string, action func() error) error {
	stack.mu.Lock()
	controller := stack.deployments
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return errors.New("service deployment runtime is not ready")
	}
	return controller.WithServiceQuiesced(ctx, serviceID, action)
}

func (stack *runtimeStack) ServiceStatus(serviceID string, enabled bool) (string, string) {
	if !enabled {
		return "disabled", ""
	}
	stack.mu.Lock()
	controller := stack.deployments
	failure := stack.serviceFailures[serviceID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil {
		return classifyServiceStatus(false, deployment.RuntimeStatus{}, false, nil, failure)
	}
	runtimeStatus, active, err := controller.Status(serviceID)
	return classifyServiceStatus(true, runtimeStatus, active, err, failure)
}

func classifyServiceStatus(runtimeReady bool, runtimeStatus deployment.RuntimeStatus, active bool, inspectErr, failure error) (string, string) {
	if !runtimeReady {
		if errors.Is(failure, deployment.ErrImageUploadRequired) {
			return "pending", "Waiting for an image upload"
		}
		if errors.Is(failure, state.ErrServiceChanged) {
			return "pending", "Applying updated configuration"
		}
		if failure != nil {
			return "failed", failure.Error()
		}
		return "pending", "Runtime is not ready"
	}
	if inspectErr != nil {
		if failure != nil {
			return "failed", failure.Error()
		}
		return "failed", inspectErr.Error()
	}
	if active && runtimeStatus.State == "running" {
		if failure != nil {
			if errors.Is(failure, deployment.ErrImageUploadRequired) {
				return "degraded", "Waiting for an image upload"
			}
			if errors.Is(failure, state.ErrServiceChanged) {
				return "degraded", "Applying updated configuration"
			}
			return "degraded", failure.Error()
		}
		return "running", ""
	}
	if active {
		if failure != nil {
			return "failed", failure.Error()
		}
		return "failed", fmt.Sprintf("Container is %s (exit code %d)", runtimeStatus.State, runtimeStatus.ExitCode)
	}
	if errors.Is(failure, deployment.ErrImageUploadRequired) {
		return "pending", "Waiting for an image upload"
	}
	if errors.Is(failure, state.ErrServiceChanged) {
		return "pending", "Applying updated configuration"
	}
	if failure != nil {
		return "failed", failure.Error()
	}
	return "pending", "Waiting for the first successful deployment"
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
	if stack.closed {
		return errors.New("container runtime is closed")
	}
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
	project, exists := stack.firewallProjects[service.ProjectID]
	if !exists {
		return fmt.Errorf("project %s firewall runtime is unavailable", service.ProjectID)
	}
	previousEndpoints := slices.Clone(project.PublicTrafficEndpoints)
	project.PublicTrafficEndpoints = slices.DeleteFunc(project.PublicTrafficEndpoints, func(endpoint firewall.PublicTrafficEndpoint) bool {
		return endpoint.ServiceID == service.ID
	})
	project.PublicTrafficEndpoints = append(project.PublicTrafficEndpoints, firewall.PublicTrafficEndpoint{
		ServiceID: service.ID, Address: address,
	})
	if err := stack.applyFirewallProjectLocked(project); err != nil {
		return err
	}
	stack.firewallProjects[service.ProjectID] = project
	if err := zone.Set(service.Name+"."+service.ProjectName+".internal", address); err != nil {
		project.PublicTrafficEndpoints = previousEndpoints
		if rollbackErr := stack.applyFirewallProjectLocked(project); rollbackErr == nil {
			stack.firewallProjects[service.ProjectID] = project
		} else {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	stack.publishBackendLocked(service.ID, publishedBackend{
		deploymentID: service.ActiveDeploymentID,
		address:      address.String(),
	})
	if stack.serviceRestarts != nil {
		stack.serviceRestarts.Publish(service.ID, service.ActiveDeploymentID, container.ID)
	}
	return nil
}

func (stack *runtimeStack) Withdraw(service state.ServiceDesired) error {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.serviceRestarts != nil {
		stack.serviceRestarts.Withdraw(service.ID)
	}
	zone := stack.dnsZones[service.ProjectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", service.ProjectID)
	}
	project, exists := stack.firewallProjects[service.ProjectID]
	if !exists {
		return fmt.Errorf("project %s firewall runtime is unavailable", service.ProjectID)
	}
	project.PublicTrafficEndpoints = slices.DeleteFunc(
		slices.Clone(project.PublicTrafficEndpoints),
		func(endpoint firewall.PublicTrafficEndpoint) bool { return endpoint.ServiceID == service.ID },
	)
	if err := stack.applyFirewallProjectLocked(project); err != nil {
		return err
	}
	stack.firewallProjects[service.ProjectID] = project
	stack.withdrawBackendLocked(service.ID)
	return zone.Delete(service.Name + "." + service.ProjectName + ".internal")
}

func (stack *runtimeStack) ServiceBackend(serviceID string, targetPort int) (deployment.Backend, bool, error) {
	if serviceID == "" || targetPort < 1 || targetPort > 65535 {
		return deployment.Backend{}, false, nil
	}
	snapshot := stack.publishedBackends.Load()
	if snapshot == nil {
		return deployment.Backend{}, false, nil
	}
	published, exists := snapshot.services[serviceID]
	if !exists {
		return deployment.Backend{}, false, nil
	}
	return deployment.Backend{
		DeploymentID: published.deploymentID,
		Address:      published.address,
		Port:         targetPort,
	}, true, nil
}

func (stack *runtimeStack) publishBackendLocked(serviceID string, backend publishedBackend) {
	current := stack.publishedBackends.Load()
	size := 1
	if current != nil {
		size += len(current.services)
	}
	services := make(map[string]publishedBackend, size)
	if current != nil {
		for currentServiceID, currentBackend := range current.services {
			services[currentServiceID] = currentBackend
		}
	}
	services[serviceID] = backend
	stack.publishedBackends.Store(&serviceBackendSnapshot{services: services})
}

func (stack *runtimeStack) withdrawBackendLocked(serviceID string) {
	current := stack.publishedBackends.Load()
	if current == nil {
		return
	}
	services := make(map[string]publishedBackend, max(len(current.services)-1, 0))
	for currentServiceID, currentBackend := range current.services {
		if currentServiceID != serviceID {
			services[currentServiceID] = currentBackend
		}
	}
	stack.publishedBackends.Store(&serviceBackendSnapshot{services: services})
}

func (stack *runtimeStack) PreviewBackend(previewID string, targetPort int) (deployment.Backend, bool, error) {
	stack.mu.Lock()
	application := stack.previews
	closed := stack.closed
	stack.mu.Unlock()
	if closed || application == nil {
		return deployment.Backend{}, false, nil
	}
	return application.Backend(previewID, targetPort)
}

func (stack *runtimeStack) previewPlacement(service state.ServiceDesired) (preview.Placement, error) {
	placement, err := stack.servicePlacement(service)
	if err != nil {
		return preview.Placement{}, err
	}
	return preview.Placement{
		NetworkName: placement.NetworkName, Gateway: placement.Gateway,
		DNSSearch: placement.DNSSearch, CgroupParent: placement.CgroupParent,
	}, nil
}

func (stack *runtimeStack) recordServiceFailure(serviceID string, err error) {
	stack.recordServiceResult(serviceID, err)
}

func (stack *runtimeStack) recordServiceResult(serviceID string, err error) {
	if errors.Is(err, deployment.ErrBlockedPair) {
		return
	}
	waitingForUpload := errors.Is(err, deployment.ErrImageUploadRequired)
	configOverride := errors.Is(err, state.ErrServiceChanged)
	stack.mu.Lock()
	previous := stack.serviceFailures[serviceID]
	switch {
	case err == nil:
		delete(stack.serviceFailures, serviceID)
	case configOverride:
		// Keep a real failure visible while the override deploy is in flight.
		if previous == nil || errors.Is(previous, state.ErrServiceChanged) || errors.Is(previous, deployment.ErrImageUploadRequired) {
			stack.serviceFailures[serviceID] = err
		}
	default:
		stack.serviceFailures[serviceID] = err
	}
	stack.mu.Unlock()
	if err == nil && previous != nil {
		systemevent.Info(
			"service_failure_cleared",
			systemevent.String("service_id", serviceID),
			systemevent.String("previous_error", previous.Error()),
		)
		return
	}
	if err != nil && !waitingForUpload && !configOverride && (previous == nil || previous.Error() != err.Error()) {
		systemevent.Failure(
			"service_failure_recorded",
			err,
			systemevent.String("service_id", serviceID),
		)
	}
}
