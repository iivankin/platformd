package errortracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
	"github.com/iivankin/platformd/internal/volumestore"
)

type Runtime interface {
	ErrorTrackerGateway(string) (netip.Addr, error)
	PublishErrorTracker(string, string) error
}

type ManagerConfig struct {
	Binary      string
	VolumeRoot  string
	RuntimeRoot string
	Runtime     Runtime
}

type runningTracker struct {
	process   *Process
	publicURL string
}

type Manager struct {
	binary      string
	volumeRoot  string
	runtimeRoot string
	runtime     Runtime
	proxy       *Proxy
	mu          sync.Mutex
	processes   map[string]runningTracker
	failures    map[string]error
	gateways    map[string]*projectGateway
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Binary == "" || config.VolumeRoot == "" || config.RuntimeRoot == "" || config.Runtime == nil {
		return nil, errors.New("error tracker manager configuration is incomplete")
	}
	manager := &Manager{
		binary: config.Binary, volumeRoot: config.VolumeRoot,
		runtimeRoot: config.RuntimeRoot, runtime: config.Runtime,
		processes: make(map[string]runningTracker), failures: make(map[string]error),
		gateways: make(map[string]*projectGateway),
	}
	proxy, err := NewProxy(manager)
	if err != nil {
		return nil, err
	}
	manager.proxy = proxy
	return manager, nil
}

func (manager *Manager) Reconcile(ctx context.Context, trackers []state.ErrorTracker) error {
	var failures []error
	for _, tracker := range trackers {
		if err := manager.Enable(ctx, tracker); err != nil {
			manager.recordFailure(tracker.ID, err)
			failures = append(failures, fmt.Errorf("start error tracker %s: %w", tracker.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (manager *Manager) Enable(ctx context.Context, tracker state.ErrorTracker) error {
	if tracker.ID == "" || tracker.ProjectID == "" || tracker.ProjectName == "" || tracker.Name == "" || tracker.VolumeID == "" {
		return errors.New("error tracker desired state is incomplete")
	}
	reference := state.PersistentVolumeReference{
		ProjectID: tracker.ProjectID, VolumeID: tracker.VolumeID, Kind: state.PersistentVolumeErrorTracker,
	}
	if _, err := volumestore.EnsureOrdinary(manager.volumeRoot, reference); err != nil {
		return fmt.Errorf("prepare error tracker volume: %w", err)
	}
	hostname := internalHostname(tracker)
	if err := manager.runtime.PublishErrorTracker(tracker.ProjectID, hostname); err != nil {
		return err
	}
	if err := manager.ensureGateway(tracker.ProjectID); err != nil {
		return err
	}
	publicURL := trackerPublicURL(tracker)
	manager.mu.Lock()
	running, exists := manager.processes[tracker.ID]
	manager.mu.Unlock()
	if exists && running.publicURL == publicURL {
		manager.setRoute(tracker.ProjectID, hostname, tracker.ID)
		manager.recordFailure(tracker.ID, nil)
		return nil
	}
	if exists {
		if err := running.process.Close(); err != nil {
			return fmt.Errorf("stop previous error tracker process: %w", err)
		}
		manager.mu.Lock()
		delete(manager.processes, tracker.ID)
		manager.mu.Unlock()
	}
	process, err := StartProcess(ctx, ProcessConfig{
		Binary:      manager.binary,
		Volume:      filepath.Join(manager.volumeRoot, tracker.ProjectID, tracker.VolumeID),
		RuntimeRoot: manager.runtimeRoot, TrackerID: tracker.ID, Slug: tracker.Name,
		PublicURL: publicURL,
	})
	if err != nil {
		return err
	}
	manager.mu.Lock()
	manager.processes[tracker.ID] = runningTracker{process: process, publicURL: publicURL}
	delete(manager.failures, tracker.ID)
	manager.mu.Unlock()
	manager.setRoute(tracker.ProjectID, hostname, tracker.ID)
	go manager.watch(tracker.ID, process)
	return nil
}

func (manager *Manager) ensureGateway(projectID string) error {
	manager.mu.Lock()
	if manager.gateways[projectID] != nil {
		manager.mu.Unlock()
		return nil
	}
	manager.mu.Unlock()
	address, err := manager.runtime.ErrorTrackerGateway(projectID)
	if err != nil {
		return err
	}
	gateway, err := startProjectGateway(address, manager.proxy)
	if err != nil {
		return fmt.Errorf("start project error tracker gateway: %w", err)
	}
	manager.mu.Lock()
	if current := manager.gateways[projectID]; current != nil {
		manager.mu.Unlock()
		_ = gateway.Close()
		return nil
	}
	manager.gateways[projectID] = gateway
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) setRoute(projectID, hostname, trackerID string) {
	manager.mu.Lock()
	gateway := manager.gateways[projectID]
	manager.mu.Unlock()
	if gateway != nil {
		gateway.Set(hostname, trackerID)
	}
}

func (manager *Manager) watch(trackerID string, process *Process) {
	<-process.Done()
	manager.mu.Lock()
	running, exists := manager.processes[trackerID]
	if exists && running.process == process {
		delete(manager.processes, trackerID)
		err := process.WaitError()
		if err == nil {
			err = errors.New("error tracker process stopped unexpectedly")
		}
		manager.failures[trackerID] = err
	}
	manager.mu.Unlock()
}

func (manager *Manager) Target(trackerID string) (*url.URL, bool) {
	manager.mu.Lock()
	running, exists := manager.processes[trackerID]
	manager.mu.Unlock()
	if !exists {
		return nil, false
	}
	return running.process.Target(), true
}

func (manager *Manager) Proxy() *Proxy { return manager.proxy }

func (manager *Manager) Status(trackerID string) (string, string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.failures[trackerID]; err != nil {
		return "failed", err.Error()
	}
	if _, exists := manager.processes[trackerID]; exists {
		return "running", ""
	}
	return "pending", "Error tracker process is not ready"
}

func (manager *Manager) recordFailure(trackerID string, err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err == nil {
		delete(manager.failures, trackerID)
	} else {
		manager.failures[trackerID] = err
	}
}

func (manager *Manager) StopProject(trackers []state.ErrorTracker) error {
	var failures []error
	for _, tracker := range trackers {
		manager.mu.Lock()
		running, exists := manager.processes[tracker.ID]
		delete(manager.processes, tracker.ID)
		delete(manager.failures, tracker.ID)
		manager.mu.Unlock()
		if exists {
			failures = append(failures, running.process.Close())
		}
	}
	if len(trackers) > 0 {
		projectID := trackers[0].ProjectID
		manager.mu.Lock()
		gateway := manager.gateways[projectID]
		delete(manager.gateways, projectID)
		manager.mu.Unlock()
		if gateway != nil {
			failures = append(failures, gateway.Close())
		}
	}
	return errors.Join(failures...)
}

func (manager *Manager) RestoreVolume(ctx context.Context, tracker state.ErrorTracker, source io.Reader) error {
	manager.mu.Lock()
	running, exists := manager.processes[tracker.ID]
	delete(manager.processes, tracker.ID)
	manager.mu.Unlock()
	if exists {
		if err := running.process.Close(); err != nil {
			return err
		}
	}
	stored := state.Volume{ID: tracker.VolumeID, ProjectID: tracker.ProjectID}
	if err := volume.RestoreBackup(ctx, manager.volumeRoot, stored, source); err != nil {
		manager.recordFailure(tracker.ID, err)
		return err
	}
	return manager.Enable(ctx, tracker)
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	processes := manager.processes
	gateways := manager.gateways
	manager.processes = make(map[string]runningTracker)
	manager.gateways = make(map[string]*projectGateway)
	manager.mu.Unlock()
	var failures []error
	for _, gateway := range gateways {
		failures = append(failures, gateway.Close())
	}
	for _, running := range processes {
		failures = append(failures, running.process.Close())
	}
	return errors.Join(failures...)
}

func internalHostname(tracker state.ErrorTracker) string {
	return tracker.Name + "." + tracker.ProjectName + ".internal"
}

func trackerPublicURL(tracker state.ErrorTracker) string {
	if tracker.PublicHostname != "" {
		return "https://" + tracker.PublicHostname
	}
	return "http://" + internalHostname(tracker) + ":" + strconv.Itoa(firewall.ErrorTrackerPort)
}
