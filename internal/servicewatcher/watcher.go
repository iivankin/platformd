package servicewatcher

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

const defaultConcurrency = 4

type Store interface {
	DesiredService(context.Context, string) (state.ServiceDesired, error)
	EnabledServiceIDs(context.Context) ([]string, error)
}

type Deployer interface {
	DeployService(context.Context, string, bool) error
}

type Config struct {
	Store                  Store
	Deployer               Deployer
	IsEmbedded             func(string) bool
	RemoteInterval         time.Duration
	RemoteMaximumBackoff   time.Duration
	EmbeddedRetry          time.Duration
	EmbeddedMaximumBackoff time.Duration
	Concurrency            int
}

type Watcher struct {
	store                  Store
	deployer               Deployer
	isEmbedded             func(string) bool
	remoteInterval         time.Duration
	remoteMaximumBackoff   time.Duration
	embeddedRetry          time.Duration
	embeddedMaximumBackoff time.Duration
	slots                  chan struct{}

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	services map[string]*serviceLoop
}

type serviceLoop struct {
	wake      chan struct{}
	reset     chan time.Duration
	stop      chan struct{}
	reference string
	embedded  bool
}

func New(config Config) (*Watcher, error) {
	if config.Store == nil || config.Deployer == nil || config.IsEmbedded == nil {
		return nil, errors.New("service watcher store, deployer, and embedded classifier are required")
	}
	if config.RemoteInterval == 0 {
		config.RemoteInterval = RemoteInterval
	}
	if config.RemoteMaximumBackoff == 0 {
		config.RemoteMaximumBackoff = RemoteMaximumBackoff
	}
	if config.EmbeddedRetry == 0 {
		config.EmbeddedRetry = EmbeddedRetry
	}
	if config.EmbeddedMaximumBackoff == 0 {
		config.EmbeddedMaximumBackoff = EmbeddedMaximumRetry
	}
	if config.Concurrency == 0 {
		config.Concurrency = defaultConcurrency
	}
	if config.RemoteInterval <= 0 || config.RemoteMaximumBackoff < config.RemoteInterval ||
		config.EmbeddedRetry <= 0 || config.EmbeddedMaximumBackoff < config.EmbeddedRetry || config.Concurrency < 1 {
		return nil, errors.New("service watcher timing or concurrency configuration is invalid")
	}
	return &Watcher{
		store: config.Store, deployer: config.Deployer, isEmbedded: config.IsEmbedded,
		remoteInterval: config.RemoteInterval, remoteMaximumBackoff: config.RemoteMaximumBackoff,
		embeddedRetry: config.EmbeddedRetry, embeddedMaximumBackoff: config.EmbeddedMaximumBackoff,
		slots: make(chan struct{}, config.Concurrency), services: make(map[string]*serviceLoop),
	}, nil
}

func (watcher *Watcher) Start(ctx context.Context, initiallyFailed func(string) bool) error {
	if ctx == nil {
		return errors.New("service watcher context is nil")
	}
	serviceIDs, err := watcher.store.EnabledServiceIDs(ctx)
	if err != nil {
		return err
	}
	watcher.mu.Lock()
	if watcher.started {
		watcher.mu.Unlock()
		return errors.New("service watcher is already started")
	}
	runContext, cancel := context.WithCancel(ctx)
	watcher.ctx = runContext
	watcher.cancel = cancel
	watcher.started = true
	watcher.mu.Unlock()
	for _, serviceID := range serviceIDs {
		failed := initiallyFailed != nil && initiallyFailed(serviceID)
		if err := watcher.Track(ctx, serviceID, failed); err != nil {
			watcher.Close()
			return err
		}
	}
	return nil
}

func (watcher *Watcher) Close() {
	watcher.mu.Lock()
	cancel := watcher.cancel
	watcher.cancel = nil
	watcher.started = false
	watcher.services = make(map[string]*serviceLoop)
	watcher.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Track registers a service after its immediate create/update reconcile. A
// failed embedded reconcile gets a bounded retry; a successful embedded tag
// service sleeps until its repository commit callback wakes it.
func (watcher *Watcher) Track(ctx context.Context, serviceID string, retry bool) error {
	desired, err := watcher.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	reference := servicesource.ImageReference(desired.Snapshot.Source)
	if !desired.Enabled || !desired.Snapshot.Source.AutoUpdate ||
		(reference != "" && serviceconfig.IsDigestReference(reference)) {
		watcher.stopService(serviceID)
		return nil
	}
	watcher.mu.Lock()
	if !watcher.started || watcher.ctx == nil {
		watcher.mu.Unlock()
		return errors.New("service watcher is not started")
	}
	if existing := watcher.services[serviceID]; existing != nil {
		existing.reference = reference
		existing.embedded = desired.Snapshot.Source.Type == servicesource.RegistryImage
		watcher.mu.Unlock()
		resetDelay(existing.reset, watcher.initialDelay(existing, retry))
		return nil
	}
	loop := &serviceLoop{
		wake: make(chan struct{}, 1), reset: make(chan time.Duration, 1), stop: make(chan struct{}),
		reference: reference,
		embedded:  desired.Snapshot.Source.Type == servicesource.RegistryImage,
	}
	watcher.services[serviceID] = loop
	runContext := watcher.ctx
	watcher.mu.Unlock()
	go watcher.runService(runContext, serviceID, loop, retry)
	return nil
}

func (watcher *Watcher) NotifyService(serviceID string) {
	watcher.mu.Lock()
	loop := watcher.services[serviceID]
	watcher.mu.Unlock()
	if loop != nil {
		coalesce(loop.wake)
	}
}

// NotifyEmbedded is called only after the embedded registry committed the tag.
// Exact normalized references prevent unrelated services from waking.
func (watcher *Watcher) NotifyEmbedded(imageReference string) {
	watcher.mu.Lock()
	loops := make([]*serviceLoop, 0)
	for _, loop := range watcher.services {
		if loop.embedded && loop.reference == imageReference {
			loops = append(loops, loop)
		}
	}
	watcher.mu.Unlock()
	for _, loop := range loops {
		coalesce(loop.wake)
	}
}

// Reclassify applies a changed embedded-registry hostname to every tracked
// reference without recreating watcher loops or persisting derived state.
func (watcher *Watcher) Reclassify() {
	watcher.mu.Lock()
	loops := make([]*serviceLoop, 0, len(watcher.services))
	for _, loop := range watcher.services {
		loop.embedded = watcher.isEmbedded(loop.reference)
		loops = append(loops, loop)
	}
	watcher.mu.Unlock()
	for _, loop := range loops {
		resetDelay(loop.reset, watcher.normalDelay(loop))
	}
}

func (watcher *Watcher) runService(ctx context.Context, serviceID string, loop *serviceLoop, retry bool) {
	defer watcher.remove(serviceID, loop)
	delay := watcher.initialDelay(loop, retry)
	failures := 0
	for {
		deploy, nextDelay, ok := waitForAction(ctx, loop.wake, loop.reset, loop.stop, delay)
		if !ok {
			return
		}
		if !deploy {
			delay = nextDelay
			continue
		}
		select {
		case watcher.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		err := watcher.deployer.DeployService(ctx, serviceID, false)
		<-watcher.slots

		desired, loadErr := watcher.store.DesiredService(ctx, serviceID)
		if loadErr != nil || !desired.Enabled {
			return
		}
		reference := servicesource.ImageReference(desired.Snapshot.Source)
		if !desired.Snapshot.Source.AutoUpdate || (reference != "" && serviceconfig.IsDigestReference(reference)) {
			return
		}
		watcher.updateLoop(loop, reference)
		if errors.Is(err, deployment.ErrBlockedPair) {
			failures = 0
			delay = watcher.normalDelay(loop)
			continue
		}
		if err != nil {
			failures++
			delay = watcher.failureDelay(loop, failures)
			continue
		}
		failures = 0
		delay = watcher.normalDelay(loop)
	}
}

func (watcher *Watcher) initialDelay(loop *serviceLoop, retry bool) time.Duration {
	if retry {
		return watcher.failureDelay(loop, 1)
	}
	return watcher.normalDelay(loop)
}

func (watcher *Watcher) normalDelay(loop *serviceLoop) time.Duration {
	watcher.mu.Lock()
	embedded := loop.embedded
	watcher.mu.Unlock()
	if embedded {
		return 0
	}
	return watcher.remoteInterval
}

func (watcher *Watcher) failureDelay(loop *serviceLoop, failures int) time.Duration {
	watcher.mu.Lock()
	embedded := loop.embedded
	watcher.mu.Unlock()
	if embedded {
		return exponentialDelay(watcher.embeddedRetry, watcher.embeddedMaximumBackoff, failures)
	}
	return exponentialDelay(watcher.remoteInterval, watcher.remoteMaximumBackoff, failures)
}

func (watcher *Watcher) updateLoop(loop *serviceLoop, reference string) {
	watcher.mu.Lock()
	loop.reference = reference
	watcher.mu.Unlock()
}

func (watcher *Watcher) remove(serviceID string, loop *serviceLoop) {
	watcher.mu.Lock()
	if watcher.services[serviceID] == loop {
		delete(watcher.services, serviceID)
	}
	watcher.mu.Unlock()
}

func (watcher *Watcher) stopService(serviceID string) {
	watcher.mu.Lock()
	loop := watcher.services[serviceID]
	if loop != nil {
		delete(watcher.services, serviceID)
		close(loop.stop)
	}
	watcher.mu.Unlock()
}
