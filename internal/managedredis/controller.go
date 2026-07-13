package managedredis

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const (
	Port                = 6379
	defaultReadyTimeout = 60 * time.Second
	defaultProbePeriod  = 250 * time.Millisecond
	defaultDrainTimeout = 2 * time.Second
	backupSaveTimeout   = 30 * time.Minute
	stopTimeoutSeconds  = 10
)

type Store interface {
	ManagedRedis(context.Context, string) (state.ManagedRedis, error)
	ManagedRedisResources(context.Context) ([]state.ManagedRedis, error)
	SwitchManagedRedisVolume(context.Context, state.SwitchManagedRedisVolume) error
}

type Engine interface {
	Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error)
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
}

type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type MaintenanceGate interface {
	BlockDatabase(context.Context, string, netip.Addr, uint16) (func() error, error)
}

type RedisConnection interface {
	Ping(context.Context) error
	Save(context.Context) error
	KillNormalClients(context.Context) error
	BeginBackgroundSave(context.Context) error
	PersistenceStatus(context.Context) (PersistenceStatus, error)
	ScanKeys(context.Context, ScanQuery) (KeyPage, error)
	PreviewKey(context.Context, PreviewQuery) (Preview, error)
	Mutate(context.Context, Mutation) (MutationResult, error)
	Close() error
}

type PersistenceStatus struct {
	BackgroundSaveInProgress bool
	LastBackgroundSaveOK     bool
}

type Placement struct {
	NetworkName  string
	Gateway      netip.Addr
	DNSSearch    string
	CgroupParent string
}

type Publisher interface {
	PublishRedis(state.ManagedRedis, containerengine.Container) error
	WithdrawRedis(state.ManagedRedis) error
}

type Config struct {
	Store            Store
	Engine           Engine
	Publisher        Publisher
	Growth           GrowthGate
	Maintenance      MaintenanceGate
	Admission        *admission.Gate
	Password         func(state.ManagedRedis) (string, error)
	Placement        func(state.ManagedRedis) (Placement, error)
	Dial             func(context.Context, string, string) (RedisConnection, error)
	GeneratedRoot    string
	VolumeRoot       string
	LogRoot          string
	LogSizeBytes     int64
	LogMaxFiles      uint
	ReadyTimeout     time.Duration
	ProbePeriod      time.Duration
	MaintenanceDrain time.Duration
	Now              func() time.Time
	NewID            func(time.Time) (string, error)
}

type activeRuntime struct {
	resource  state.ManagedRedis
	container containerengine.Container
	network   string
	runtimeID string
}

type Controller struct {
	store            Store
	engine           Engine
	publisher        Publisher
	growth           GrowthGate
	maintenance      MaintenanceGate
	admission        *admission.Gate
	password         func(state.ManagedRedis) (string, error)
	placement        func(state.ManagedRedis) (Placement, error)
	dial             func(context.Context, string, string) (RedisConnection, error)
	generatedRoot    string
	volumeRoot       string
	logRoot          string
	logSizeBytes     int64
	logMaxFiles      uint
	readyTimeout     time.Duration
	probePeriod      time.Duration
	maintenanceDrain time.Duration
	now              func() time.Time
	newID            func(time.Time) (string, error)

	mu          sync.Mutex
	locks       map[string]*sync.Mutex
	active      map[string]activeRuntime
	maintaining map[string]struct{}
}

func NewController(config Config) (*Controller, error) {
	if config.Store == nil || config.Engine == nil || config.Publisher == nil || config.Growth == nil || config.Maintenance == nil || config.Admission == nil || config.Password == nil || config.Placement == nil {
		return nil, errors.New("managed Redis controller dependencies are incomplete")
	}
	if !safeRoot(config.GeneratedRoot) || !safeRoot(config.VolumeRoot) || !safeRoot(config.LogRoot) {
		return nil, errors.New("managed Redis controller roots must be canonical absolute non-root paths")
	}
	if config.LogSizeBytes <= 0 || config.LogMaxFiles == 0 {
		return nil, errors.New("managed Redis log rotation must be positive")
	}
	dial := config.Dial
	if dial == nil {
		dial = func(ctx context.Context, address, password string) (RedisConnection, error) {
			return Dial(ctx, address, password)
		}
	}
	readyTimeout := config.ReadyTimeout
	if readyTimeout == 0 {
		readyTimeout = defaultReadyTimeout
	}
	probePeriod := config.ProbePeriod
	if probePeriod == 0 {
		probePeriod = defaultProbePeriod
	}
	maintenanceDrain := config.MaintenanceDrain
	if maintenanceDrain == 0 {
		maintenanceDrain = defaultDrainTimeout
	}
	if readyTimeout < 0 || probePeriod < 0 || maintenanceDrain < 0 {
		return nil, errors.New("managed Redis readiness timing cannot be negative")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = func(timestamp time.Time) (string, error) { return id.NewWith(timestamp, rand.Reader) }
	}
	return &Controller{
		store: config.Store, engine: config.Engine, publisher: config.Publisher, growth: config.Growth, maintenance: config.Maintenance, admission: config.Admission,
		password: config.Password, placement: config.Placement, dial: dial,
		generatedRoot: config.GeneratedRoot, volumeRoot: config.VolumeRoot, logRoot: config.LogRoot,
		logSizeBytes: config.LogSizeBytes, logMaxFiles: config.LogMaxFiles,
		readyTimeout: readyTimeout, probePeriod: probePeriod, maintenanceDrain: maintenanceDrain, now: now, newID: newID,
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeRuntime), maintaining: make(map[string]struct{}),
	}, nil
}

func (controller *Controller) RestoreAll(ctx context.Context) error {
	resources, err := controller.store.ManagedRedisResources(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, resource := range resources {
		if err := controller.Start(ctx, resource.ID); err != nil {
			failures = append(failures, fmt.Errorf("start managed Redis %s: %w", resource.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (controller *Controller) Start(ctx context.Context, resourceID string) error {
	lease, err := controller.admission.Begin("redis_start", resourceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()
	if _, active := controller.activeRuntime(resourceID); active {
		return nil
	}
	resource, err := controller.store.ManagedRedis(ctx, resourceID)
	if err != nil {
		return err
	}
	password, err := controller.password(resource)
	if err != nil {
		return fmt.Errorf("open managed Redis password: %w", err)
	}
	if !validPassword(password) {
		return errors.New("managed Redis password has an invalid generated format")
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return fmt.Errorf("place managed Redis runtime: %w", err)
	}
	image, err := controller.resolveImage(ctx, resource)
	if err != nil {
		return err
	}
	volume, err := ensureVolume(controller.volumeRoot, resource.ProjectID, resource.VolumeID)
	if err != nil {
		return err
	}
	configPath, err := writeConfig(controller.generatedRoot, resource.ID, password)
	if err != nil {
		return err
	}
	container, err := controller.createContainer(ctx, resource, image.ID, placement, volume, configPath)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		if remove {
			_ = controller.engine.RemoveContainer(context.Background(), container.ID, true)
		}
	}()
	if err := controller.engine.StartContainer(ctx, container.ID); err != nil {
		return fmt.Errorf("start managed Redis container: %w", err)
	}
	ready, err := controller.waitReady(ctx, container.ID, placement.NetworkName, password)
	if err != nil {
		return err
	}
	if err := controller.publisher.PublishRedis(resource, ready); err != nil {
		return fmt.Errorf("publish managed Redis: %w", err)
	}
	controller.setActive(resource.ID, activeRuntime{
		resource: resource, container: ready, network: placement.NetworkName, runtimeID: resource.ID,
	})
	remove = false
	return nil
}

func (controller *Controller) Stop(ctx context.Context, resourceID string) error {
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return nil
	}
	withdrawErr := controller.publisher.WithdrawRedis(active.resource)
	password, passwordErr := controller.password(active.resource)
	saveErr := passwordErr
	if passwordErr == nil {
		saveErr = controller.finalSave(ctx, active, password)
	}
	stopErr := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds)
	removeErr := controller.engine.RemoveContainer(ctx, active.container.ID, true)
	controller.clearActive(resourceID)
	return errors.Join(withdrawErr, saveErr, stopErr, removeErr)
}

func (controller *Controller) StopAll(ctx context.Context) error {
	controller.mu.Lock()
	ids := make([]string, 0, len(controller.active))
	for id := range controller.active {
		ids = append(ids, id)
	}
	controller.mu.Unlock()
	sort.Strings(ids)
	var failures []error
	for _, id := range ids {
		if err := controller.Stop(ctx, id); err != nil {
			failures = append(failures, fmt.Errorf("stop managed Redis %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func (controller *Controller) QuiesceAll(ctx context.Context) (func(context.Context) error, error) {
	controller.mu.Lock()
	ids := make([]string, 0, len(controller.active))
	for resourceID := range controller.active {
		ids = append(ids, resourceID)
	}
	controller.mu.Unlock()
	sort.Strings(ids)
	stopped := make([]activeRuntime, 0, len(ids))
	for _, resourceID := range ids {
		active, err := controller.quiesce(ctx, resourceID)
		if active != nil {
			stopped = append(stopped, *active)
		}
		if err != nil {
			return controller.resumeAll(stopped), err
		}
	}
	return controller.resumeAll(stopped), nil
}

func (controller *Controller) quiesce(ctx context.Context, resourceID string) (*activeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return nil, nil
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return nil, err
	}
	if err := controller.finalSave(ctx, active, password); err != nil {
		return nil, err
	}
	if err := controller.publisher.WithdrawRedis(active.resource); err != nil {
		return nil, err
	}
	if err := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds); err != nil {
		return nil, errors.Join(err, controller.publisher.PublishRedis(active.resource, active.container))
	}
	controller.clearActive(resourceID)
	return &active, nil
}

func (controller *Controller) resumeAll(runtimes []activeRuntime) func(context.Context) error {
	return func(ctx context.Context) error {
		var failures []error
		for _, runtime := range runtimes {
			if err := controller.resume(ctx, runtime); err != nil {
				failures = append(failures, fmt.Errorf("resume managed Redis %s: %w", runtime.resource.ID, err))
			}
		}
		return errors.Join(failures...)
	}
}

func (controller *Controller) resume(ctx context.Context, runtime activeRuntime) error {
	lock := controller.resourceLock(runtime.resource.ID)
	lock.Lock()
	defer lock.Unlock()
	password, err := controller.password(runtime.resource)
	if err != nil {
		return err
	}
	if err := controller.engine.StartContainer(ctx, runtime.container.ID); err != nil {
		return err
	}
	ready, err := controller.waitReady(ctx, runtime.container.ID, runtime.network, password)
	if err != nil {
		return err
	}
	runtime.container = ready
	controller.setActive(runtime.resource.ID, runtime)
	return controller.publisher.PublishRedis(runtime.resource, ready)
}

func (controller *Controller) Status(resourceID string) (containerengine.Container, bool, error) {
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return containerengine.Container{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	return container, true, err
}

// OpenBackupRDB starts a Redis background save and returns a descriptor for the
// newly renamed dump.rdb. The resource lock is released only after opening the
// file, so later Redis saves may replace the pathname without changing the
// bytes read through this stable descriptor.
func (controller *Controller) OpenBackupRDB(ctx context.Context, resourceID string) (io.ReadCloser, error) {
	lease, err := controller.admission.Begin("redis_backup_snapshot", resourceID)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return nil, ErrNotRunning
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return nil, err
	}
	return controller.openFreshRDB(ctx, active, password)
}

func (controller *Controller) openFreshRDB(
	ctx context.Context,
	active activeRuntime,
	password string,
) (*os.File, error) {
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return nil, err
	}
	addresses := container.IPs[active.network]
	if container.State != "running" || len(addresses) != 1 {
		return nil, ErrNotRunning
	}
	connection, err := controller.dial(ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	saveContext, cancel := context.WithTimeout(ctx, backupSaveTimeout)
	defer cancel()
	if err := controller.waitForBackgroundSave(saveContext, connection, false, nil); err != nil {
		return nil, err
	}
	path := filepath.Join(controller.volumeRoot, active.resource.ProjectID, active.resource.VolumeID, "dump.rdb")
	previous, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := connection.BeginBackgroundSave(saveContext); err != nil {
		return nil, err
	}
	file, err := controller.waitAndOpenBackgroundSave(saveContext, connection, path, previous)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (controller *Controller) waitAndOpenBackgroundSave(
	ctx context.Context,
	connection RedisConnection,
	path string,
	previous os.FileInfo,
) (*os.File, error) {
	var opened *os.File
	err := controller.waitForBackgroundSave(ctx, connection, true, func(status PersistenceStatus) (bool, error) {
		if status.BackgroundSaveInProgress {
			return false, nil
		}
		if !status.LastBackgroundSaveOK {
			return false, errors.New("Redis background save failed")
		}
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			_ = file.Close()
			return false, errors.Join(statErr, errors.New("Redis backup RDB is empty or not a regular file"))
		}
		if previous != nil && os.SameFile(previous, info) {
			_ = file.Close()
			return false, nil
		}
		current, statErr := os.Stat(path)
		if statErr != nil || !os.SameFile(info, current) {
			_ = file.Close()
			return false, statErr
		}
		opened = file
		return true, nil
	})
	return opened, err
}

func (controller *Controller) waitForBackgroundSave(
	ctx context.Context,
	connection RedisConnection,
	requireNewFile bool,
	ready func(PersistenceStatus) (bool, error),
) error {
	ticker := time.NewTicker(controller.probePeriod)
	defer ticker.Stop()
	for {
		status, err := connection.PersistenceStatus(ctx)
		if err != nil {
			return err
		}
		if !requireNewFile && !status.BackgroundSaveInProgress {
			return nil
		}
		if ready != nil {
			done, err := ready(status)
			if err != nil || done {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (controller *Controller) ScanKeys(ctx context.Context, resourceID string, query ScanQuery) (KeyPage, error) {
	active, ok, maintenance := controller.availableRuntime(resourceID)
	if maintenance {
		return KeyPage{}, ErrMaintenance
	}
	if !ok {
		return KeyPage{}, ErrNotRunning
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return KeyPage{}, err
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return KeyPage{}, err
	}
	addresses := container.IPs[active.network]
	if container.State != "running" || len(addresses) != 1 {
		return KeyPage{}, ErrNotRunning
	}
	connection, err := controller.dial(ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password)
	if err != nil {
		return KeyPage{}, err
	}
	defer connection.Close()
	return connection.ScanKeys(ctx, query)
}

func (controller *Controller) PreviewKey(ctx context.Context, resourceID string, query PreviewQuery) (Preview, error) {
	active, ok, maintenance := controller.availableRuntime(resourceID)
	if maintenance {
		return Preview{}, ErrMaintenance
	}
	if !ok {
		return Preview{}, ErrNotRunning
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return Preview{}, err
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return Preview{}, err
	}
	addresses := container.IPs[active.network]
	if container.State != "running" || len(addresses) != 1 {
		return Preview{}, ErrNotRunning
	}
	connection, err := controller.dial(ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password)
	if err != nil {
		return Preview{}, err
	}
	defer connection.Close()
	return connection.PreviewKey(ctx, query)
}

func (controller *Controller) Mutate(ctx context.Context, resourceID string, mutation Mutation) (MutationResult, error) {
	lease, err := controller.admission.Begin("redis_mutation", resourceID)
	if err != nil {
		return MutationResult{}, err
	}
	defer lease.Release()
	active, ok, maintenance := controller.availableRuntime(resourceID)
	if maintenance {
		return MutationResult{}, ErrMaintenance
	}
	if !ok {
		return MutationResult{}, ErrNotRunning
	}
	password, err := controller.password(active.resource)
	if err != nil {
		return MutationResult{}, err
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return MutationResult{}, err
	}
	addresses := container.IPs[active.network]
	if container.State != "running" || len(addresses) != 1 {
		return MutationResult{}, ErrNotRunning
	}
	connection, err := controller.dial(ctx, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password)
	if err != nil {
		return MutationResult{}, err
	}
	defer connection.Close()
	return connection.Mutate(ctx, mutation)
}

func (controller *Controller) createContainer(ctx context.Context, resource state.ManagedRedis, imageID string, placement Placement, volume, configPath string) (containerengine.Container, error) {
	return controller.createContainerAttempt(ctx, resource, resource.ID, imageID, placement, volume, configPath)
}

func (controller *Controller) createContainerAttempt(
	ctx context.Context,
	resource state.ManagedRedis,
	runtimeID string,
	imageID string,
	placement Placement,
	volume string,
	configPath string,
) (containerengine.Container, error) {
	if !safePathComponent(runtimeID) {
		return containerengine.Container{}, errors.New("managed Redis runtime ID is invalid")
	}
	attemptID, err := controller.newID(controller.now())
	if err != nil {
		return containerengine.Container{}, fmt.Errorf("allocate managed Redis runtime attempt ID: %w", err)
	}
	logPath := filepath.Join(controller.logRoot, "redis", resource.ID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return containerengine.Container{}, fmt.Errorf("create managed Redis log directory: %w", err)
	}
	return controller.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-redis-" + runtimeID,
		Command: []string{"redis-server", "/run/platformd/redis.conf"},
		Labels: map[string]string{
			"io.platformd.owner": "redis", "io.platformd.project-id": resource.ProjectID,
			"io.platformd.redis-id": resource.ID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch: []string{placement.DNSSearch},
		Mounts: []containerengine.Mount{
			{Source: volume, Destination: "/data"},
			{Source: configPath, Destination: "/run/platformd/redis.conf", ReadOnly: true},
		},
		LogPath: logPath, LogSizeBytes: controller.logSizeBytes, LogMaxFiles: controller.logMaxFiles,
		CgroupParent: placement.CgroupParent, CPUMillicores: resource.CPUMillicores,
		MemoryMaxBytes: resource.MemoryMaxBytes,
	})
}

func (controller *Controller) waitReady(ctx context.Context, containerID, networkName, password string) (containerengine.Container, error) {
	deadline := time.Now().Add(controller.readyTimeout)
	ticker := time.NewTicker(controller.probePeriod)
	defer ticker.Stop()
	var lastProbeErr error
	for {
		container, err := controller.engine.InspectContainer(containerID)
		if err != nil {
			return containerengine.Container{}, err
		}
		if container.State != "running" {
			return containerengine.Container{}, fmt.Errorf("managed Redis container state is %s", container.State)
		}
		addresses := container.IPs[networkName]
		if len(addresses) == 1 {
			address := net.JoinHostPort(addresses[0], fmt.Sprint(Port))
			connection, dialErr := controller.dial(ctx, address, password)
			if dialErr == nil {
				pingErr := connection.Ping(ctx)
				_ = connection.Close()
				if pingErr == nil {
					return container, nil
				}
				lastProbeErr = pingErr
			} else {
				lastProbeErr = dialErr
			}
		}
		if !time.Now().Before(deadline) {
			if lastProbeErr != nil {
				return containerengine.Container{}, fmt.Errorf("managed Redis readiness timed out: %w", lastProbeErr)
			}
			return containerengine.Container{}, errors.New("managed Redis readiness timed out before a single project address was assigned")
		}
		select {
		case <-ctx.Done():
			return containerengine.Container{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (controller *Controller) finalSave(ctx context.Context, active activeRuntime, password string) error {
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return err
	}
	addresses := container.IPs[active.network]
	if len(addresses) != 1 {
		return fmt.Errorf("managed Redis container has %d project addresses, want one", len(addresses))
	}
	saveContext, cancel := context.WithTimeout(ctx, stopTimeoutSeconds*time.Second)
	defer cancel()
	connection, err := controller.dial(saveContext, net.JoinHostPort(addresses[0], fmt.Sprint(Port)), password)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Save(saveContext)
}

func (controller *Controller) resourceLock(resourceID string) *sync.Mutex {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	lock := controller.locks[resourceID]
	if lock == nil {
		lock = &sync.Mutex{}
		controller.locks[resourceID] = lock
	}
	return lock
}

func (controller *Controller) activeRuntime(resourceID string) (activeRuntime, bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	active, ok := controller.active[resourceID]
	return active, ok
}

func (controller *Controller) availableRuntime(resourceID string) (activeRuntime, bool, bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	_, maintenance := controller.maintaining[resourceID]
	active, exists := controller.active[resourceID]
	return active, exists, maintenance
}

func (controller *Controller) beginMaintenance(resourceID string) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if _, exists := controller.maintaining[resourceID]; exists {
		return false
	}
	controller.maintaining[resourceID] = struct{}{}
	return true
}

func (controller *Controller) endMaintenance(resourceID string) {
	controller.mu.Lock()
	delete(controller.maintaining, resourceID)
	controller.mu.Unlock()
}

func (controller *Controller) setActive(resourceID string, active activeRuntime) {
	controller.mu.Lock()
	controller.active[resourceID] = active
	controller.mu.Unlock()
}

func (controller *Controller) clearActive(resourceID string) {
	controller.mu.Lock()
	delete(controller.active, resourceID)
	controller.mu.Unlock()
}
