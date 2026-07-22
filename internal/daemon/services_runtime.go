package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"strings"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/preview"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/servicerestart"
	"github.com/iivankin/platformd/internal/servicewatcher"
	"github.com/iivankin/platformd/internal/state"
)

const (
	serviceLogSegmentBytes = 10 << 20
	serviceLogMaxFiles     = 3
)

func (stack *runtimeStack) ConfigureDeployments(ctx context.Context, store *state.Store, master cryptobox.MasterKey, credentials deployment.CredentialResolver, registryApplication *registry.Application, githubApplication *githubapp.Application, adminHostname string) error {
	var imageSources deployment.ImageSourceResolver
	if registryApplication != nil {
		imageSources = embeddedImageSourceResolver{
			runtime: stack, application: registryApplication, generatedRoot: stack.paths.GeneratedRoot,
		}
	}
	controller, err := deployment.New(deployment.Config{
		Store: store, Engine: stack.engine, Publisher: stack, Credentials: credentials,
		Environment:  resourceVariableResolver{store: store, master: master},
		ImageSources: imageSources, Growth: stack.growth, Admission: stack.admission,
		Sources: githubSourceResolver{
			github: githubApplication, engine: stack.engine, generatedRoot: stack.paths.GeneratedRoot,
			buildNetwork: stack.buildNetwork.Name,
		},
		Reporter:  githubDeploymentReporter{github: githubApplication, adminHostname: adminHostname},
		Placement: stack.servicePlacement,
		LogRoot:   stack.paths.LogsRoot, VolumeRoot: stack.paths.VolumesRoot,
		LogSizeBytes: serviceLogSegmentBytes, LogMaxFiles: serviceLogMaxFiles,
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

func (stack *runtimeStack) ConfigureServiceWatcher(ctx context.Context, store *state.Store, embeddedRegistryHost string) error {
	stack.mu.Lock()
	stack.embeddedRegistryHost = embeddedRegistryHost
	stack.mu.Unlock()
	watcher, err := servicewatcher.New(servicewatcher.Config{
		Store: store, Deployer: stack,
		IsEmbedded: stack.isEmbeddedReference,
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

func (stack *runtimeStack) SetEmbeddedRegistryHost(hostname string) {
	stack.mu.Lock()
	stack.embeddedRegistryHost = hostname
	watcher := stack.serviceWatcher
	stack.mu.Unlock()
	if watcher != nil {
		watcher.Reclassify()
	}
}

func (stack *runtimeStack) isEmbeddedReference(reference string) bool {
	host, err := imagecredential.HostForReference(reference)
	if err != nil {
		return false
	}
	stack.mu.Lock()
	embedded := stack.embeddedRegistryHost
	stack.mu.Unlock()
	return embedded != "" && host == embedded
}

func (stack *runtimeStack) RegistryTagPublished(repository, tag string) {
	stack.mu.Lock()
	hostname := stack.embeddedRegistryHost
	watcher := stack.serviceWatcher
	stack.mu.Unlock()
	if hostname != "" && watcher != nil {
		watcher.NotifyEmbedded(hostname + "/" + repository + ":" + tag)
	}
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

func (stack *runtimeStack) NotifyEmbeddedImage(imageReference string) {
	stack.mu.Lock()
	watcher := stack.serviceWatcher
	stack.mu.Unlock()
	if watcher != nil {
		watcher.NotifyEmbedded(imageReference)
	}
}

func (stack *runtimeStack) NotifyGitHubPush(
	ctx context.Context,
	store *state.Store,
	application *githubapp.Application,
	event githubapp.PushEvent,
) {
	serviceIDs, err := store.EnabledServiceIDs(ctx)
	if err != nil {
		return
	}
	for _, serviceID := range serviceIDs {
		desired, err := store.DesiredService(ctx, serviceID)
		if err != nil || desired.Snapshot.Source.GitHub == nil {
			continue
		}
		source := desired.Snapshot.Source.GitHub
		if source.RepositoryID != event.RepositoryID || source.Branch != event.Branch {
			continue
		}
		// Waiting services are driven by check webhooks, while ordinary services
		// are driven by pushes. This avoids racing a push against check creation.
		if source.WaitForCI != event.ChecksEvent {
			continue
		}
		changedPaths := event.ChangedPaths
		if event.ChecksEvent && len(source.TriggerPaths) > 0 {
			commit, err := application.Commit(ctx, event.RepositoryID, event.Revision)
			if err != nil {
				stack.recordServiceFailure(serviceID, err)
				continue
			}
			changedPaths = commit.ChangedPaths
		}
		if !githubPathsMatch(source.TriggerPaths, changedPaths) {
			continue
		}
		go func(serviceID string) {
			stack.mu.Lock()
			controller := stack.deployments
			stack.mu.Unlock()
			if controller == nil {
				stack.recordServiceFailure(serviceID, errors.New("service deployment runtime is not configured"))
				return
			}
			if err := controller.DeployRevision(ctx, serviceID, event.Revision, false); err != nil &&
				!errors.Is(err, deployment.ErrSourceChecksPending) && !errors.Is(err, deployment.ErrBlockedPair) {
				stack.recordServiceFailure(serviceID, err)
			}
		}(serviceID)
	}
}

func githubPathsMatch(filters, changed []string) bool {
	if len(filters) == 0 || len(changed) == 0 {
		return true
	}
	for _, filter := range filters {
		prefix := strings.TrimSuffix(filter, "/")
		for _, path := range changed {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func (stack *runtimeStack) hasServiceFailure(serviceID string) bool {
	stack.mu.Lock()
	defer stack.mu.Unlock()
	return stack.serviceFailures[serviceID] != nil
}

func (stack *runtimeStack) DeployService(ctx context.Context, serviceID string, force bool) error {
	return stack.deployService(ctx, serviceID, func(controller *deployment.Controller) error {
		return controller.Deploy(ctx, serviceID, force)
	})
}

func (stack *runtimeStack) DeployServiceRevision(ctx context.Context, serviceID, revision string, force bool) error {
	return stack.deployService(ctx, serviceID, func(controller *deployment.Controller) error {
		return controller.DeployRevision(ctx, serviceID, revision, force)
	})
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
	stack.mu.Lock()
	if err == nil {
		delete(stack.serviceFailures, serviceID)
	} else {
		stack.serviceFailures[serviceID] = err
	}
	stack.mu.Unlock()
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
	stack.mu.Lock()
	if err == nil {
		delete(stack.serviceFailures, serviceID)
	} else {
		stack.serviceFailures[serviceID] = err
	}
	stack.mu.Unlock()
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
	stack.mu.Lock()
	if err == nil {
		delete(stack.serviceFailures, service.ID)
	} else {
		stack.serviceFailures[service.ID] = err
	}
	stack.mu.Unlock()
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
	if err := zone.Set(service.Name+"."+service.ProjectName+".internal", address); err != nil {
		return err
	}
	stack.publishedServices[service.ID] = true
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
	delete(stack.publishedServices, service.ID)
	zone := stack.dnsZones[service.ProjectID]
	if zone == nil {
		return fmt.Errorf("project %s DNS runtime is unavailable", service.ProjectID)
	}
	return zone.Delete(service.Name + "." + service.ProjectName + ".internal")
}

func (stack *runtimeStack) ServiceBackend(serviceID string, targetPort int) (deployment.Backend, bool, error) {
	stack.mu.Lock()
	controller := stack.deployments
	published := stack.publishedServices[serviceID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || controller == nil || !published {
		return deployment.Backend{}, false, nil
	}
	return controller.Backend(serviceID, targetPort)
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
	stack.mu.Lock()
	stack.serviceFailures[serviceID] = err
	stack.mu.Unlock()
}

func (stack *runtimeStack) recordServiceResult(serviceID string, err error) {
	stack.mu.Lock()
	if err == nil {
		delete(stack.serviceFailures, serviceID)
	} else {
		stack.serviceFailures[serviceID] = err
	}
	stack.mu.Unlock()
}
