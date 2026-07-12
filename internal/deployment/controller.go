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

type Config struct {
	Store        Store
	Engine       Engine
	Publisher    Publisher
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
}

type Controller struct {
	store        Store
	engine       Engine
	publisher    Publisher
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
		store: config.Store, engine: config.Engine, publisher: config.Publisher,
		placement: config.Placement, logRoot: config.LogRoot, volumeRoot: config.VolumeRoot,
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
	if normalized.RegistryCredentialID != "" {
		return errors.New("registry credential resolution is not configured")
	}

	image, err := controller.engine.Pull(ctx, containerengine.PullRequest{
		Reference: normalized.ImageReference,
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

func (controller *Controller) runDeployment(ctx context.Context, desired state.ServiceDesired, deploymentID, imageID string) error {
	placement, err := controller.placement(desired)
	if err != nil {
		return controller.fail(deploymentID, "placement_failed", err)
	}
	attemptID, err := controller.newID(controller.now())
	if err != nil {
		return controller.fail(deploymentID, "attempt_id_failed", err)
	}
	logPath := filepath.Join(controller.logRoot, "services", desired.ID, deploymentID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return controller.fail(deploymentID, "log_directory_failed", err)
	}
	mounts := make([]containerengine.Mount, 0, len(desired.Snapshot.VolumeMounts))
	for _, mount := range desired.Snapshot.VolumeMounts {
		mounts = append(mounts, containerengine.Mount{
			Source:      filepath.Join(controller.volumeRoot, desired.ProjectID, mount.VolumeID),
			Destination: mount.ContainerPath,
		})
	}
	candidate, err := controller.engine.CreateContainer(ctx, containerengine.ContainerSpec{
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
		return controller.fail(deploymentID, "candidate_create_failed", err)
	}
	candidateActive := true
	defer func() {
		if candidateActive {
			_ = controller.engine.RemoveContainer(context.Background(), candidate.ID, true)
		}
	}()

	old, hasOld := controller.activeContainer(desired.ID)
	if desired.ActiveDeploymentID != "" && (!hasOld || old.deploymentID != desired.ActiveDeploymentID) {
		return controller.fail(deploymentID, "active_runtime_missing", errors.New("active deployment has no matching runtime container"))
	}
	if hasOld {
		if err := controller.engine.StopContainer(old.container.ID, stopTimeoutSeconds); err != nil {
			return controller.fail(deploymentID, "old_stop_failed", err)
		}
		if err := controller.publisher.Withdraw(desired); err != nil {
			controller.restoreOld(desired, old, true)
			return controller.fail(deploymentID, "publication_withdraw_failed", err)
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
	controller.setActive(desired.ID, activeContainer{deploymentID: deploymentID, container: ready})
	candidateActive = false
	if err := controller.publisher.Publish(desired, ready); err != nil {
		return fmt.Errorf("publish ready service after deployment commit: %w", err)
	}
	if hasOld {
		_ = controller.engine.RemoveContainer(context.Background(), old.container.ID, true)
	}
	return nil
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
