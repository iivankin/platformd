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
	Store                Store
	Deployer             Deployer
	RemoteInterval       time.Duration
	RemoteMaximumBackoff time.Duration
	Concurrency          int
}

type Watcher struct {
	store                Store
	deployer             Deployer
	remoteInterval       time.Duration
	remoteMaximumBackoff time.Duration
	slots                chan struct{}

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	services map[string]*serviceLoop
}

type serviceLoop struct {
	wake       chan struct{}
	reset      chan time.Duration
	stop       chan struct{}
	generation uint64
}

func New(config Config) (*Watcher, error) {
	if config.Store == nil || config.Deployer == nil {
		return nil, errors.New("service watcher store and deployer are required")
	}
	if config.RemoteInterval == 0 {
		config.RemoteInterval = RemoteInterval
	}
	if config.RemoteMaximumBackoff == 0 {
		config.RemoteMaximumBackoff = RemoteMaximumBackoff
	}
	if config.Concurrency == 0 {
		config.Concurrency = defaultConcurrency
	}
	if config.RemoteInterval <= 0 || config.RemoteMaximumBackoff < config.RemoteInterval ||
		config.Concurrency < 1 {
		return nil, errors.New("service watcher timing or concurrency configuration is invalid")
	}
	return &Watcher{
		store: config.Store, deployer: config.Deployer,
		remoteInterval: config.RemoteInterval, remoteMaximumBackoff: config.RemoteMaximumBackoff,
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

// Track registers a service after its immediate create/update reconcile.
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
		watcher.mu.Unlock()
		resetDelay(existing.reset, watcher.initialDelay(existing, retry))
		return nil
	}
	loop := &serviceLoop{
		wake: make(chan struct{}, 1), reset: make(chan time.Duration, 1), stop: make(chan struct{}),
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

// Reconcile schedules one immediate deployment from durable desired state.
// Unlike Track, it also creates a short-lived loop for disabled, digest-pinned,
// and non-auto-updating services so HTTP mutations never wait for image work.
func (watcher *Watcher) Reconcile(ctx context.Context, serviceID string) error {
	watcher.mu.Lock()
	if !watcher.started || watcher.ctx == nil {
		watcher.mu.Unlock()
		return errors.New("service watcher is not started")
	}
	loop := watcher.services[serviceID]
	if loop == nil {
		loop = &serviceLoop{
			wake: make(chan struct{}, 1), reset: make(chan time.Duration, 1), stop: make(chan struct{}),
			generation: 1,
		}
		watcher.services[serviceID] = loop
		runContext := watcher.ctx
		go watcher.runService(runContext, serviceID, loop, false)
	} else {
		loop.generation++
	}
	watcher.mu.Unlock()
	coalesce(loop.wake)
	return nil
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
		watcher.mu.Lock()
		generation := loop.generation
		watcher.mu.Unlock()
		err := watcher.deployer.DeployService(ctx, serviceID, false)
		<-watcher.slots

		desired, loadErr := watcher.store.DesiredService(ctx, serviceID)
		if loadErr != nil {
			return
		}
		reference := servicesource.ImageReference(desired.Snapshot.Source)
		if !desired.Enabled || !desired.Snapshot.Source.AutoUpdate ||
			(reference != "" && serviceconfig.IsDigestReference(reference)) {
			if watcher.finishOneShot(serviceID, loop, generation) {
				return
			}
			delay = 0
			continue
		}
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

// finishOneShot removes a completed on-demand loop only when no newer
// mutation arrived while it was deploying. The generation check closes the
// otherwise tiny race where a coalesced update could be lost during teardown.
func (watcher *Watcher) finishOneShot(serviceID string, loop *serviceLoop, generation uint64) bool {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.services[serviceID] != loop || loop.generation != generation {
		return false
	}
	delete(watcher.services, serviceID)
	return true
}

func (watcher *Watcher) initialDelay(loop *serviceLoop, retry bool) time.Duration {
	if retry {
		return watcher.failureDelay(loop, 1)
	}
	return watcher.normalDelay(loop)
}

func (watcher *Watcher) normalDelay(loop *serviceLoop) time.Duration {
	return watcher.remoteInterval
}

func (watcher *Watcher) failureDelay(loop *serviceLoop, failures int) time.Duration {
	return exponentialDelay(watcher.remoteInterval, watcher.remoteMaximumBackoff, failures)
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
