package servicerestart

import (
	"context"
	"errors"
	"sync"
	"time"
)

var defaultBackoff = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

type Engine interface {
	WaitContainer(context.Context, string) (int32, error)
}

type Controller interface {
	PrepareUnexpectedExit(context.Context, string, string, string) (bool, error)
	RestoreCurrent(context.Context, string, string) (bool, error)
}

type Config struct {
	Context       context.Context
	Engine        Engine
	Controller    Controller
	Backoff       []time.Duration
	StableRuntime time.Duration
	WaitRetry     time.Duration
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
	OnResult      func(string, error)
}

type monitor struct {
	deploymentID string
	containerID  string
	startedAt    time.Time
	cancel       context.CancelFunc
}

type crashState struct {
	deploymentID string
	attempts     int
}

type Manager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	engine        Engine
	controller    Controller
	backoff       []time.Duration
	stableRuntime time.Duration
	waitRetry     time.Duration
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	onResult      func(string, error)

	mu       sync.Mutex
	closed   bool
	monitors map[string]monitor
	crashes  map[string]crashState
}

func New(config Config) (*Manager, error) {
	if config.Context == nil || config.Engine == nil || config.Controller == nil {
		return nil, errors.New("service restart manager dependencies are incomplete")
	}
	backoff := config.Backoff
	if len(backoff) == 0 {
		backoff = defaultBackoff
	}
	for _, delay := range backoff {
		if delay <= 0 {
			return nil, errors.New("service restart delays must be positive")
		}
	}
	stableRuntime := config.StableRuntime
	if stableRuntime == 0 {
		stableRuntime = 5 * time.Minute
	}
	waitRetry := config.WaitRetry
	if waitRetry == 0 {
		waitRetry = time.Second
	}
	if stableRuntime < 0 || waitRetry < 0 {
		return nil, errors.New("service restart timing must not be negative")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	onResult := config.OnResult
	if onResult == nil {
		onResult = func(string, error) {}
	}
	ctx, cancel := context.WithCancel(config.Context)
	return &Manager{
		ctx: ctx, cancel: cancel, engine: config.Engine, controller: config.Controller,
		backoff: append([]time.Duration(nil), backoff...), stableRuntime: stableRuntime,
		waitRetry: waitRetry, now: now, sleep: sleep, onResult: onResult,
		monitors: make(map[string]monitor), crashes: make(map[string]crashState),
	}, nil
}

func (manager *Manager) Publish(serviceID, deploymentID, containerID string) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	if current, exists := manager.monitors[serviceID]; exists {
		if current.deploymentID == deploymentID && current.containerID == containerID {
			manager.mu.Unlock()
			return
		}
		current.cancel()
	}
	if state := manager.crashes[serviceID]; state.deploymentID != "" && state.deploymentID != deploymentID {
		delete(manager.crashes, serviceID)
	}
	ctx, cancel := context.WithCancel(manager.ctx)
	entry := monitor{deploymentID: deploymentID, containerID: containerID, startedAt: manager.now(), cancel: cancel}
	manager.monitors[serviceID] = entry
	manager.mu.Unlock()
	go manager.wait(ctx, serviceID, entry)
}

func (manager *Manager) Withdraw(serviceID string) {
	manager.mu.Lock()
	if current, exists := manager.monitors[serviceID]; exists {
		current.cancel()
		delete(manager.monitors, serviceID)
	}
	manager.mu.Unlock()
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	manager.cancel()
	for serviceID, current := range manager.monitors {
		current.cancel()
		delete(manager.monitors, serviceID)
	}
	manager.mu.Unlock()
}

func (manager *Manager) wait(ctx context.Context, serviceID string, entry monitor) {
	for {
		_, err := manager.engine.WaitContainer(ctx, entry.containerID)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			manager.handleExit(serviceID, entry)
			return
		}
		if manager.sleep(ctx, manager.waitRetry) != nil {
			return
		}
	}
}

func (manager *Manager) handleExit(serviceID string, entry monitor) {
	manager.mu.Lock()
	current, exists := manager.monitors[serviceID]
	if manager.closed || !exists || current.deploymentID != entry.deploymentID || current.containerID != entry.containerID {
		manager.mu.Unlock()
		return
	}
	delete(manager.monitors, serviceID)
	stable := manager.now().Sub(entry.startedAt) >= manager.stableRuntime
	if stable {
		delete(manager.crashes, serviceID)
	}
	manager.mu.Unlock()

	prepared, err := manager.controller.PrepareUnexpectedExit(manager.ctx, serviceID, entry.deploymentID, entry.containerID)
	if err != nil {
		manager.onResult(serviceID, err)
	}
	if !prepared || manager.ctx.Err() != nil {
		return
	}
	for {
		delay := manager.nextDelay(serviceID, entry.deploymentID)
		if manager.sleep(manager.ctx, delay) != nil {
			return
		}
		restored, restoreErr := manager.controller.RestoreCurrent(manager.ctx, serviceID, entry.deploymentID)
		if restoreErr != nil {
			manager.onResult(serviceID, restoreErr)
			continue
		}
		if !restored {
			return
		}
		manager.onResult(serviceID, nil)
		return
	}
}

func (manager *Manager) nextDelay(serviceID, deploymentID string) time.Duration {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.crashes[serviceID]
	if state.deploymentID != deploymentID {
		state = crashState{deploymentID: deploymentID}
	}
	index := min(state.attempts, len(manager.backoff)-1)
	delay := manager.backoff[index]
	state.attempts++
	manager.crashes[serviceID] = state
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
