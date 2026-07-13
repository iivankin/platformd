package managedpostgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

const (
	defaultReadyTimeout = 90 * time.Second
	defaultProbePeriod  = 250 * time.Millisecond
	stopTimeoutSeconds  = 30
)

type ControllerStore interface {
	ManagedPostgres(context.Context, string) (state.ManagedPostgres, error)
	ManagedPostgresResources(context.Context) ([]state.ManagedPostgres, error)
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

type Connection interface {
	Bootstrap(context.Context, string, string, string) error
	Ping(context.Context) error
	Query(context.Context, string) (QueryResult, error)
	Close(context.Context) error
}

type Placement struct {
	NetworkName  string
	Gateway      netip.Addr
	DNSSearch    string
	CgroupParent string
}

type Publisher interface {
	PublishPostgres(state.ManagedPostgres, containerengine.Container) error
	WithdrawPostgres(state.ManagedPostgres) error
}

type ControllerConfig struct {
	Store             ControllerStore
	Engine            Engine
	Publisher         Publisher
	Growth            GrowthGate
	OwnerPassword     func(state.ManagedPostgres) (string, error)
	BootstrapPassword func(state.ManagedPostgres) (string, error)
	Placement         func(state.ManagedPostgres) (Placement, error)
	Dial              func(context.Context, string, string, string, string) (Connection, error)
	VolumeRoot        string
	LogRoot           string
	LogSizeBytes      int64
	LogMaxFiles       uint
	ReadyTimeout      time.Duration
	ProbePeriod       time.Duration
	Now               func() time.Time
	NewID             func(time.Time) (string, error)
}

type activeRuntime struct {
	resource  state.ManagedPostgres
	container containerengine.Container
	network   string
}

type Controller struct {
	store             ControllerStore
	engine            Engine
	publisher         Publisher
	growth            GrowthGate
	ownerPassword     func(state.ManagedPostgres) (string, error)
	bootstrapPassword func(state.ManagedPostgres) (string, error)
	placement         func(state.ManagedPostgres) (Placement, error)
	dial              func(context.Context, string, string, string, string) (Connection, error)
	volumeRoot        string
	logRoot           string
	logSizeBytes      int64
	logMaxFiles       uint
	readyTimeout      time.Duration
	probePeriod       time.Duration
	now               func() time.Time
	newID             func(time.Time) (string, error)
	mu                sync.Mutex
	locks             map[string]*sync.Mutex
	active            map[string]activeRuntime
}

func NewController(config ControllerConfig) (*Controller, error) {
	if config.Store == nil || config.Engine == nil || config.Publisher == nil || config.Growth == nil || config.OwnerPassword == nil || config.BootstrapPassword == nil || config.Placement == nil {
		return nil, errors.New("managed PostgreSQL controller dependencies are incomplete")
	}
	if !safeRoot(config.VolumeRoot) || !safeRoot(config.LogRoot) || config.LogSizeBytes <= 0 || config.LogMaxFiles == 0 {
		return nil, errors.New("managed PostgreSQL runtime paths and log rotation are invalid")
	}
	dial := config.Dial
	if dial == nil {
		dial = func(ctx context.Context, address, username, password, database string) (Connection, error) {
			return Dial(ctx, address, username, password, database)
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
	if readyTimeout < 0 || probePeriod < 0 {
		return nil, errors.New("managed PostgreSQL readiness timing cannot be negative")
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
		store: config.Store, engine: config.Engine, publisher: config.Publisher, growth: config.Growth,
		ownerPassword: config.OwnerPassword, bootstrapPassword: config.BootstrapPassword,
		placement: config.Placement, dial: dial, volumeRoot: config.VolumeRoot,
		logRoot: config.LogRoot, logSizeBytes: config.LogSizeBytes, logMaxFiles: config.LogMaxFiles,
		readyTimeout: readyTimeout, probePeriod: probePeriod, now: now, newID: newID,
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeRuntime),
	}, nil
}

func (controller *Controller) Start(ctx context.Context, resourceID string) error {
	lock := controller.resourceLock(resourceID)
	lock.Lock()
	defer lock.Unlock()
	if _, active := controller.activeRuntime(resourceID); active {
		return nil
	}
	resource, err := controller.store.ManagedPostgres(ctx, resourceID)
	if err != nil {
		return err
	}
	ownerPassword, err := controller.ownerPassword(resource)
	if err != nil {
		return fmt.Errorf("open managed PostgreSQL owner password: %w", err)
	}
	bootstrapPassword, err := controller.bootstrapPassword(resource)
	if err != nil {
		return fmt.Errorf("open managed PostgreSQL bootstrap password: %w", err)
	}
	placement, err := controller.placement(resource)
	if err != nil {
		return fmt.Errorf("place managed PostgreSQL runtime: %w", err)
	}
	reference, err := managedimages.Reference(managedimages.PostgreSQL, resource.ImageTag)
	if err != nil {
		return err
	}
	pinned, err := serviceconfig.PinnedReference(reference, resource.ImageDigest)
	if err != nil {
		return err
	}
	image, inspectErr := controller.engine.InspectImage(ctx, resource.ImageDigest)
	if inspectErr != nil {
		if err := controller.growth.PermitGrowth(ctx); err != nil {
			return fmt.Errorf("managed PostgreSQL image is not cached: %w", err)
		}
		image, err = controller.engine.Pull(ctx, containerengine.PullRequest{Reference: pinned})
		if err != nil {
			return fmt.Errorf("pull pinned managed PostgreSQL image: %w", err)
		}
	}
	if image.ID == "" || image.Digest != resource.ImageDigest {
		return fmt.Errorf("managed PostgreSQL image digest = %q, want %q", image.Digest, resource.ImageDigest)
	}
	volume, err := ensureVolume(controller.volumeRoot, resource.ProjectID, resource.VolumeID)
	if err != nil {
		return err
	}
	container, err := controller.createContainer(ctx, resource, image.ID, placement, volume, bootstrapPassword)
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
		return fmt.Errorf("start managed PostgreSQL container: %w", err)
	}
	ready, err := controller.waitReady(ctx, container.ID, placement.NetworkName, resource, ownerPassword, bootstrapPassword)
	if err != nil {
		return err
	}
	if err := controller.publisher.PublishPostgres(resource, ready); err != nil {
		return fmt.Errorf("publish managed PostgreSQL: %w", err)
	}
	controller.setActive(resource.ID, activeRuntime{resource: resource, container: ready, network: placement.NetworkName})
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
	withdrawErr := controller.publisher.WithdrawPostgres(active.resource)
	stopErr := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds)
	removeErr := controller.engine.RemoveContainer(ctx, active.container.ID, true)
	controller.clearActive(resourceID)
	return errors.Join(withdrawErr, stopErr, removeErr)
}

func (controller *Controller) StopAll(ctx context.Context) error {
	controller.mu.Lock()
	ids := make([]string, 0, len(controller.active))
	for resourceID := range controller.active {
		ids = append(ids, resourceID)
	}
	controller.mu.Unlock()
	sort.Strings(ids)
	var failures []error
	for _, resourceID := range ids {
		if err := controller.Stop(ctx, resourceID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (controller *Controller) Status(resourceID string) (containerengine.Container, bool, error) {
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return containerengine.Container{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	return container, true, err
}

func (controller *Controller) Query(ctx context.Context, resourceID, sql string) (QueryResult, error) {
	active, ok := controller.activeRuntime(resourceID)
	if !ok {
		return QueryResult{}, ErrNotRunning
	}
	ownerPassword, err := controller.ownerPassword(active.resource)
	if err != nil {
		return QueryResult{}, err
	}
	address, err := controller.runtimeAddress(active)
	if err != nil {
		return QueryResult{}, err
	}
	connection, err := controller.dial(ctx, address, active.resource.OwnerUsername, ownerPassword, active.resource.DatabaseName)
	if err != nil {
		return QueryResult{}, err
	}
	defer connection.Close(context.Background())
	return connection.Query(ctx, sql)
}

func (controller *Controller) createContainer(ctx context.Context, resource state.ManagedPostgres, imageID string, placement Placement, volume, bootstrapPassword string) (containerengine.Container, error) {
	attemptID, err := controller.newID(controller.now())
	if err != nil {
		return containerengine.Container{}, err
	}
	logPath := filepath.Join(controller.logRoot, "postgres", resource.ID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return containerengine.Container{}, err
	}
	return controller.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-postgres-" + resource.ID,
		Environment: map[string]string{
			"PGDATA": "/var/lib/postgresql/data/pgdata", "POSTGRES_USER": "postgres",
			"POSTGRES_DB": "postgres", "POSTGRES_PASSWORD": bootstrapPassword,
		},
		Labels: map[string]string{
			"io.platformd.owner": "postgres", "io.platformd.project-id": resource.ProjectID,
			"io.platformd.postgres-id": resource.ID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch: []string{placement.DNSSearch},
		Mounts:    []containerengine.Mount{{Source: volume, Destination: "/var/lib/postgresql/data"}},
		LogPath:   logPath, LogSizeBytes: controller.logSizeBytes, LogMaxFiles: controller.logMaxFiles,
		CgroupParent: placement.CgroupParent, CPUMillicores: resource.CPUMillicores,
		MemoryMaxBytes: resource.MemoryMaxBytes,
	})
}

func (controller *Controller) waitReady(ctx context.Context, containerID, networkName string, resource state.ManagedPostgres, ownerPassword, bootstrapPassword string) (containerengine.Container, error) {
	deadline := time.Now().Add(controller.readyTimeout)
	ticker := time.NewTicker(controller.probePeriod)
	defer ticker.Stop()
	var lastErr error
	for {
		container, err := controller.engine.InspectContainer(containerID)
		if err != nil {
			return containerengine.Container{}, err
		}
		if container.State != "running" {
			return containerengine.Container{}, fmt.Errorf("managed PostgreSQL container state is %s", container.State)
		}
		addresses := container.IPs[networkName]
		if len(addresses) == 1 {
			address := net.JoinHostPort(addresses[0], fmt.Sprint(Port))
			lastErr = controller.bootstrapAndProbe(ctx, address, resource, ownerPassword, bootstrapPassword)
			if lastErr == nil {
				return container, nil
			}
		}
		if !time.Now().Before(deadline) {
			return containerengine.Container{}, fmt.Errorf("managed PostgreSQL readiness timed out: %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return containerengine.Container{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (controller *Controller) bootstrapAndProbe(ctx context.Context, address string, resource state.ManagedPostgres, ownerPassword, bootstrapPassword string) error {
	bootstrap, err := controller.dial(ctx, address, "postgres", bootstrapPassword, "postgres")
	if err != nil {
		return err
	}
	err = bootstrap.Bootstrap(ctx, resource.DatabaseName, resource.OwnerUsername, ownerPassword)
	closeErr := bootstrap.Close(ctx)
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	owner, err := controller.dial(ctx, address, resource.OwnerUsername, ownerPassword, resource.DatabaseName)
	if err != nil {
		return err
	}
	pingErr := owner.Ping(ctx)
	return errors.Join(pingErr, owner.Close(ctx))
}

func (controller *Controller) runtimeAddress(active activeRuntime) (string, error) {
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return "", err
	}
	addresses := container.IPs[active.network]
	if container.State != "running" || len(addresses) != 1 {
		return "", ErrNotRunning
	}
	return net.JoinHostPort(addresses[0], fmt.Sprint(Port)), nil
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
