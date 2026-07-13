package deployment

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

const (
	processStartupGrace = 3 * time.Second
	probeInterval       = 250 * time.Millisecond
	probeTimeout        = 2 * time.Second
	stopTimeoutSeconds  = 10
)

var ErrBlockedPair = errors.New("deployment pair is blocked by an earlier failure")

type Store interface {
	DesiredService(context.Context, string) (state.ServiceDesired, error)
	BeginDeployment(context.Context, state.BeginDeployment) error
	ActivateDeployment(context.Context, string, string, string, int64) error
	FailDeployment(context.Context, string, string, string, int64) error
	LatestFailedDeployment(context.Context, string, string, string) (bool, error)
	Deployment(context.Context, string) (state.DeploymentRecord, error)
}

type Engine interface {
	Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
}

type Placement struct {
	NetworkName  string
	Gateway      netip.Addr
	DNSSearch    string
	CgroupParent string
}

type Publisher interface {
	Publish(state.ServiceDesired, containerengine.Container) error
	Withdraw(state.ServiceDesired) error
}

type ImageCredential struct {
	Username string
	Password string
}

type RuntimeStatus struct {
	DeploymentID string
	State        string
	ExitCode     int32
}

type Backend struct {
	DeploymentID string
	Address      string
	Port         int
}

type CredentialResolver interface {
	Resolve(context.Context, state.ServiceDesired) (ImageCredential, error)
}

type ImageSourceResolver interface {
	Resolve(context.Context, string) (reference string, close func(), handled bool, err error)
}

type Config struct {
	Store        Store
	Engine       Engine
	Publisher    Publisher
	Credentials  CredentialResolver
	ImageSources ImageSourceResolver
	Placement    func(state.ServiceDesired) (Placement, error)
	LogRoot      string
	VolumeRoot   string
	LogSizeBytes int64
	LogMaxFiles  uint
	Now          func() time.Time
	NewID        func(time.Time) (string, error)
	HTTPClient   *http.Client
}

type activeContainer struct {
	deploymentID string
	container    containerengine.Container
	networkName  string
	targetPort   int
}

type Controller struct {
	store        Store
	engine       Engine
	publisher    Publisher
	credentials  CredentialResolver
	imageSources ImageSourceResolver
	placement    func(state.ServiceDesired) (Placement, error)
	logRoot      string
	volumeRoot   string
	logSizeBytes int64
	logMaxFiles  uint
	now          func() time.Time
	newID        func(time.Time) (string, error)
	httpClient   *http.Client

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	active map[string]activeContainer
}

func New(config Config) (*Controller, error) {
	if config.Store == nil || config.Engine == nil || config.Publisher == nil || config.Placement == nil {
		return nil, errors.New("deployment controller dependencies are incomplete")
	}
	if !safeRoot(config.LogRoot) || !safeRoot(config.VolumeRoot) {
		return nil, errors.New("deployment controller roots must be canonical absolute non-root paths")
	}
	if config.LogSizeBytes <= 0 || config.LogMaxFiles == 0 {
		return nil, errors.New("deployment controller log rotation must be positive")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = func(timestamp time.Time) (string, error) {
			return id.NewWith(timestamp, rand.Reader)
		}
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: probeTimeout,
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: true,
				DialContext:       (&net.Dialer{Timeout: probeTimeout}).DialContext,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Controller{
		store: config.Store, engine: config.Engine, publisher: config.Publisher, credentials: config.Credentials,
		imageSources: config.ImageSources,
		placement:    config.Placement, logRoot: config.LogRoot, volumeRoot: config.VolumeRoot,
		logSizeBytes: config.LogSizeBytes, logMaxFiles: config.LogMaxFiles,
		now: now, newID: newID, httpClient: httpClient,
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeContainer),
	}, nil
}

func (controller *Controller) Deploy(ctx context.Context, serviceID string, force bool) error {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()

	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	if !desired.Enabled {
		return controller.stopDisabled(ctx, desired)
	}
	normalized, snapshotJSON, configHash, err := serviceconfig.Canonical(desired.Snapshot)
	if err != nil {
		return err
	}
	desired.Snapshot = normalized
	credential := ImageCredential{}
	if normalized.ImageCredentialID != "" {
		if controller.credentials == nil {
			return errors.New("image credential resolution is not configured")
		}
		credential, err = controller.credentials.Resolve(ctx, desired)
		if err != nil {
			return fmt.Errorf("resolve image credential: %w", err)
		}
	}

	image, err := controller.pull(ctx, containerengine.PullRequest{
		Reference: normalized.ImageReference,
		Username:  credential.Username,
		Password:  credential.Password,
		Refresh:   !serviceconfig.IsDigestReference(normalized.ImageReference),
	})
	if err != nil {
		return fmt.Errorf("resolve and pull service image: %w", err)
	}
	if image.ID == "" || image.Digest == "" {
		return errors.New("pulled image has no ID or digest")
	}
	if _, err := serviceconfig.PinnedReference(normalized.ImageReference, image.Digest); err != nil {
		return err
	}

	current, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	currentNormalized, _, currentHash, err := serviceconfig.Canonical(current.Snapshot)
	if err != nil {
		return err
	}
	if !current.Enabled || currentHash != configHash || currentNormalized.ImageReference != normalized.ImageReference {
		return state.ErrServiceChanged
	}
	desired = current
	desired.Snapshot = currentNormalized
	if !force && desired.ActiveImageDigest == image.Digest && desired.ActiveConfigHash == configHash {
		active, ok := controller.activeContainer(serviceID)
		if !ok || active.deploymentID != desired.ActiveDeploymentID {
			return errors.New("active deployment has no matching runtime container")
		}
		return controller.publisher.Publish(desired, active.container)
	}
	if !force {
		blocked, err := controller.store.LatestFailedDeployment(ctx, serviceID, configHash, image.Digest)
		if err != nil {
			return err
		}
		if blocked {
			return ErrBlockedPair
		}
	}

	startedAt := controller.now()
	deploymentID, err := controller.newID(startedAt)
	if err != nil {
		return fmt.Errorf("allocate deployment ID: %w", err)
	}
	if err := controller.store.BeginDeployment(ctx, state.BeginDeployment{
		ID: deploymentID, ServiceID: serviceID, ImageDigest: image.Digest,
		ConfigHash: configHash, SnapshotJSON: snapshotJSON, CreatedAtMillis: startedAt.UnixMilli(),
	}); err != nil {
		return err
	}
	return controller.runDeployment(ctx, desired, deploymentID, image.ID)
}

func (controller *Controller) Restore(ctx context.Context, serviceID string) error {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	_, err := controller.restoreCurrentLocked(ctx, serviceID, "")
	return err
}

// RestoreCurrent recreates the exact logical deployment only while it is
// still current. The expected pointer prevents a delayed crash-loop attempt
// from reviving an older deployment after a user action has replaced it.
func (controller *Controller) RestoreCurrent(ctx context.Context, serviceID, expectedDeploymentID string) (bool, error) {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	return controller.restoreCurrentLocked(ctx, serviceID, expectedDeploymentID)
}

func (controller *Controller) restoreCurrentLocked(ctx context.Context, serviceID, expectedDeploymentID string) (bool, error) {
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	if !desired.Enabled || desired.ActiveDeploymentID == "" {
		return false, nil
	}
	if expectedDeploymentID != "" && desired.ActiveDeploymentID != expectedDeploymentID {
		return false, nil
	}
	if _, exists := controller.activeContainer(serviceID); exists {
		return false, nil
	}
	activeDeployment, err := controller.store.Deployment(ctx, desired.ActiveDeploymentID)
	if err != nil {
		return false, fmt.Errorf("load active deployment: %w", err)
	}
	desired.Snapshot = activeDeployment.Snapshot
	credential := ImageCredential{}
	if desired.Snapshot.ImageCredentialID != "" {
		if controller.credentials == nil {
			return false, errors.New("image credential resolution is not configured")
		}
		credential, err = controller.credentials.Resolve(ctx, desired)
		if err != nil {
			return false, fmt.Errorf("resolve active image credential: %w", err)
		}
	}
	pinnedReference, err := serviceconfig.PinnedReference(desired.Snapshot.ImageReference, activeDeployment.ImageDigest)
	if err != nil {
		return false, err
	}
	image, err := controller.pull(ctx, containerengine.PullRequest{
		Reference: pinnedReference, Username: credential.Username, Password: credential.Password,
	})
	if err != nil {
		return false, fmt.Errorf("pull active service image: %w", err)
	}
	if image.Digest != activeDeployment.ImageDigest {
		return false, fmt.Errorf("active image digest = %s, want %s", image.Digest, activeDeployment.ImageDigest)
	}
	container, placement, err := controller.createRuntimeContainer(ctx, desired, activeDeployment.ID, image.ID)
	if err != nil {
		return false, err
	}
	remove := true
	defer func() {
		if remove {
			_ = controller.engine.RemoveContainer(context.Background(), container.ID, true)
		}
	}()
	if err := controller.engine.StartContainer(ctx, container.ID); err != nil {
		return false, fmt.Errorf("start active service container: %w", err)
	}
	ready, err := controller.waitReady(ctx, desired, container.ID, placement.NetworkName)
	if err != nil {
		return false, fmt.Errorf("restore active service readiness: %w", err)
	}
	controller.setActive(desired.ID, activeContainer{
		deploymentID: activeDeployment.ID, container: ready,
		networkName: placement.NetworkName, targetPort: targetPort(desired.Snapshot.TargetPort),
	})
	remove = false
	if err := controller.publisher.Publish(desired, ready); err != nil {
		return false, fmt.Errorf("publish restored service: %w", err)
	}
	return true, nil
}

func (controller *Controller) pull(ctx context.Context, request containerengine.PullRequest) (containerengine.Image, error) {
	if controller.imageSources == nil {
		return controller.engine.Pull(ctx, request)
	}
	reference, closeSource, handled, err := controller.imageSources.Resolve(ctx, request.Reference)
	if err != nil {
		return containerengine.Image{}, err
	}
	if !handled {
		return controller.engine.Pull(ctx, request)
	}
	if closeSource == nil || reference == "" {
		return containerengine.Image{}, errors.New("resolved image source is incomplete")
	}
	if request.Username != "" || request.Password != "" {
		closeSource()
		return containerengine.Image{}, errors.New("embedded registry image cannot use a remote image credential")
	}
	defer closeSource()
	request.Reference = reference
	request.Refresh = false
	return controller.engine.Pull(ctx, request)
}

// PrepareUnexpectedExit removes publication and the exited runtime attempt.
// Product state and the logical active deployment pointer remain unchanged.
func (controller *Controller) PrepareUnexpectedExit(ctx context.Context, serviceID, deploymentID, containerID string) (bool, error) {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	active, exists := controller.activeContainer(serviceID)
	if !desired.Enabled || desired.ActiveDeploymentID != deploymentID || !exists || active.deploymentID != deploymentID || active.container.ID != containerID {
		return false, nil
	}
	withdrawErr := controller.publisher.Withdraw(desired)
	controller.clearActive(serviceID)
	removeErr := controller.engine.RemoveContainer(ctx, containerID, true)
	return true, errors.Join(withdrawErr, removeErr)
}

func (controller *Controller) Status(serviceID string) (RuntimeStatus, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok {
		return RuntimeStatus{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return RuntimeStatus{DeploymentID: active.deploymentID}, true, err
	}
	return RuntimeStatus{
		DeploymentID: active.deploymentID,
		State:        container.State,
		ExitCode:     container.ExitCode,
	}, true, nil
}

func (controller *Controller) Backend(serviceID string) (Backend, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok || active.targetPort == 0 {
		return Backend{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return Backend{}, true, err
	}
	if container.State != "running" {
		return Backend{}, false, nil
	}
	addresses := container.IPs[active.networkName]
	if len(addresses) != 1 {
		return Backend{}, true, fmt.Errorf("service container has %d backend addresses, want one", len(addresses))
	}
	return Backend{
		DeploymentID: active.deploymentID, Address: addresses[0], Port: active.targetPort,
	}, true, nil
}

func (controller *Controller) runDeployment(ctx context.Context, desired state.ServiceDesired, deploymentID, imageID string) error {
	candidate, placement, err := controller.createRuntimeContainer(ctx, desired, deploymentID, imageID)
	if err != nil {
		return controller.fail(deploymentID, "candidate_create_failed", err)
	}
	candidateActive := true
	defer func() {
		if candidateActive {
			_ = controller.engine.RemoveContainer(context.Background(), candidate.ID, true)
		}
	}()

	old, hasOld := controller.activeContainer(desired.ID)
	if hasOld && old.deploymentID != desired.ActiveDeploymentID {
		return controller.fail(deploymentID, "active_runtime_missing", errors.New("active deployment has no matching runtime container"))
	}
	if hasOld {
		if err := controller.publisher.Withdraw(desired); err != nil {
			controller.restoreOld(desired, old, true)
			return controller.fail(deploymentID, "publication_withdraw_failed", err)
		}
		if err := controller.engine.StopContainer(old.container.ID, stopTimeoutSeconds); err != nil {
			controller.restoreOld(desired, old, true)
			return controller.fail(deploymentID, "old_stop_failed", err)
		}
	}
	if err := controller.engine.StartContainer(ctx, candidate.ID); err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "candidate_start_failed", err)
	}
	ready, err := controller.waitReady(ctx, desired, candidate.ID, placement.NetworkName)
	if err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "readiness_failed", err)
	}
	if err := controller.store.ActivateDeployment(ctx, desired.ID, deploymentID, desired.ActiveDeploymentID, controller.now().UnixMilli()); err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "publication_commit_failed", err)
	}
	desired.ActiveDeploymentID = deploymentID
	controller.setActive(desired.ID, activeContainer{
		deploymentID: deploymentID, container: ready,
		networkName: placement.NetworkName, targetPort: targetPort(desired.Snapshot.TargetPort),
	})
	candidateActive = false
	if err := controller.publisher.Publish(desired, ready); err != nil {
		return fmt.Errorf("publish ready service after deployment commit: %w", err)
	}
	if hasOld {
		_ = controller.engine.RemoveContainer(context.Background(), old.container.ID, true)
	}
	return nil
}

func (controller *Controller) createRuntimeContainer(ctx context.Context, desired state.ServiceDesired, deploymentID, imageID string) (containerengine.Container, Placement, error) {
	placement, err := controller.placement(desired)
	if err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("place service runtime: %w", err)
	}
	attemptID, err := controller.newID(controller.now())
	if err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("allocate runtime attempt ID: %w", err)
	}
	logPath := filepath.Join(controller.logRoot, "services", desired.ID, deploymentID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("create service log directory: %w", err)
	}
	mounts := make([]containerengine.Mount, 0, len(desired.Snapshot.VolumeMounts))
	for _, mount := range desired.Snapshot.VolumeMounts {
		mounts = append(mounts, containerengine.Mount{
			Source:      filepath.Join(controller.volumeRoot, desired.ProjectID, mount.VolumeID),
			Destination: mount.ContainerPath,
		})
	}
	container, err := controller.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-service-" + deploymentID,
		Entrypoint: desired.Snapshot.Command, Command: desired.Snapshot.Args,
		Environment: desired.Snapshot.Environment,
		Labels: map[string]string{
			"io.platformd.owner": "service", "io.platformd.project-id": desired.ProjectID,
			"io.platformd.service-id": desired.ID, "io.platformd.deployment-id": deploymentID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch: []string{placement.DNSSearch}, Mounts: mounts,
		LogPath: logPath, LogSizeBytes: controller.logSizeBytes, LogMaxFiles: controller.logMaxFiles,
		CgroupParent:  placement.CgroupParent,
		CPUMillicores: desired.Snapshot.CPUMillicores, MemoryMaxBytes: desired.Snapshot.MemoryMaxBytes,
	})
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	return container, placement, nil
}

func (controller *Controller) waitReady(ctx context.Context, desired state.ServiceDesired, containerID, networkName string) (containerengine.Container, error) {
	timeout := time.Duration(desired.Snapshot.StartupTimeoutSeconds) * time.Second
	deadline := controller.now().Add(timeout)
	if desired.Snapshot.HealthPath == "" {
		deadline = controller.now().Add(processStartupGrace)
	}
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	for {
		container, err := controller.engine.InspectContainer(containerID)
		if err != nil {
			return containerengine.Container{}, err
		}
		if container.State != "running" {
			return containerengine.Container{}, fmt.Errorf("container state is %s", container.State)
		}
		if desired.Snapshot.HealthPath == "" {
			if !controller.now().Before(deadline) {
				return container, nil
			}
		} else {
			addresses := container.IPs[networkName]
			if len(addresses) == 1 {
				ready, probeErr := controller.probeHTTP(ctx, addresses[0], *desired.Snapshot.TargetPort, desired.Snapshot.HealthPath)
				if ready {
					return container, nil
				}
				if probeErr != nil && !controller.now().Before(deadline) {
					return containerengine.Container{}, probeErr
				}
			}
			if !controller.now().Before(deadline) {
				return containerengine.Container{}, errors.New("HTTP readiness timed out")
			}
		}
		select {
		case <-ctx.Done():
			return containerengine.Container{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (controller *Controller) probeHTTP(ctx context.Context, address string, port int, healthPath string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(address, fmt.Sprintf("%d", port))+healthPath, nil)
	if err != nil {
		return false, err
	}
	response, err := controller.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400, nil
}

func (controller *Controller) fail(deploymentID, code string, cause error) error {
	message := cause.Error()
	if err := controller.store.FailDeployment(context.Background(), deploymentID, code, message, controller.now().UnixMilli()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (controller *Controller) stopDisabled(ctx context.Context, desired state.ServiceDesired) error {
	active, ok := controller.activeContainer(desired.ID)
	if !ok {
		return nil
	}
	if err := controller.publisher.Withdraw(desired); err != nil {
		return err
	}
	if err := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds); err != nil {
		return err
	}
	if err := controller.engine.RemoveContainer(ctx, active.container.ID, true); err != nil {
		return err
	}
	controller.clearActive(desired.ID)
	return nil
}

func (controller *Controller) restoreOld(desired state.ServiceDesired, old activeContainer, exists bool) {
	if !exists {
		return
	}
	if err := controller.engine.StartContainer(context.Background(), old.container.ID); err != nil {
		return
	}
	container, err := controller.engine.InspectContainer(old.container.ID)
	if err != nil {
		return
	}
	_ = controller.publisher.Publish(desired, container)
}

func (controller *Controller) serviceLock(serviceID string) *sync.Mutex {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	lock := controller.locks[serviceID]
	if lock == nil {
		lock = &sync.Mutex{}
		controller.locks[serviceID] = lock
	}
	return lock
}

func (controller *Controller) activeContainer(serviceID string) (activeContainer, bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	active, ok := controller.active[serviceID]
	return active, ok
}

func (controller *Controller) setActive(serviceID string, active activeContainer) {
	controller.mu.Lock()
	controller.active[serviceID] = active
	controller.mu.Unlock()
}

func (controller *Controller) clearActive(serviceID string) {
	controller.mu.Lock()
	delete(controller.active, serviceID)
	controller.mu.Unlock()
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func targetPort(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
