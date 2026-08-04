package resourcemetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/hostmetrics"
)

func NewApplication(store Store, usage UsageReader, network NetworkReader, disk DiskReader, host hostmetrics.Reader, config Config) (*Application, error) {
	if store == nil || usage == nil || network == nil || disk == nil || host == nil {
		return nil, errors.New("resource metrics dependencies are incomplete")
	}
	if config.LiveInterval == 0 {
		config.LiveInterval = LiveSampleInterval
	}
	if config.PersistInterval == 0 {
		config.PersistInterval = PersistInterval
	}
	if config.Retention == 0 {
		config.Retention = Retention
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.LiveInterval <= 0 || config.PersistInterval < config.LiveInterval || config.Retention < config.PersistInterval {
		return nil, errors.New("resource metrics intervals or retention are invalid")
	}
	return &Application{
		store: store, usage: usage, network: network, disk: disk, host: host,
		liveInterval: config.LiveInterval, persistInterval: config.PersistInterval,
		retention: config.Retention, now: config.Now,
		resources: make(map[metricKey]Current), projects: make(map[string]Current),
		previous: make(map[metricKey]previousSample),
	}, nil
}

func (application *Application) Read(kind cgroupstats.Kind, resourceID string) (Current, error) {
	if err := cgroupstats.ValidateResource(kind, resourceID); err != nil {
		return Current{}, err
	}
	application.mu.RLock()
	current, exists := application.resources[metricKey{kind: string(kind), id: resourceID}]
	application.mu.RUnlock()
	if !exists {
		return Current{}, ErrNotReady
	}
	return current, nil
}

func (application *Application) ReadProject(projectID string) (Current, error) {
	if projectID == "" {
		return Current{}, cgroupstats.ErrInvalidResource
	}
	application.mu.RLock()
	current, exists := application.projects[projectID]
	application.mu.RUnlock()
	if !exists {
		return Current{}, ErrNotReady
	}
	return current, nil
}

func (application *Application) ReadInstallation() (Current, error) {
	application.mu.RLock()
	current, ready := application.installation, application.ready
	application.mu.RUnlock()
	if !ready {
		return Current{}, ErrNotReady
	}
	return current, nil
}

func (application *Application) History(ctx context.Context, kind cgroupstats.Kind, resourceID string, window time.Duration) (History, error) {
	if err := cgroupstats.ValidateResource(kind, resourceID); err != nil {
		return History{}, err
	}
	history, step, err := application.historyWindow(window)
	if err != nil {
		return History{}, err
	}
	samples, err := application.store.ResourceMetricSamples(ctx, string(kind), resourceID, history.From, history.To)
	if err != nil {
		return History{}, err
	}
	history.Points = aggregateResources(samples, history.From, step)
	return history, nil
}

func (application *Application) ProjectHistory(ctx context.Context, projectID string, window time.Duration) (History, error) {
	if projectID == "" {
		return History{}, cgroupstats.ErrInvalidResource
	}
	history, step, err := application.historyWindow(window)
	if err != nil {
		return History{}, err
	}
	series, err := application.store.ResourceMetricSeriesByProject(ctx, projectID, history.From, history.To)
	if err != nil {
		return History{}, err
	}
	for _, item := range series {
		history.Series = append(history.Series, HistorySeries{
			ID: item.ResourceID, Kind: item.Kind, Name: item.Name,
			Points: aggregateResources(item.Samples, history.From, step),
		})
	}
	return history, nil
}

func (application *Application) InstallationHistory(ctx context.Context, window time.Duration) (History, error) {
	history, step, err := application.historyWindow(window)
	if err != nil {
		return History{}, err
	}
	series, err := application.store.ProjectAggregateMetricSeries(ctx, history.From, history.To)
	if err != nil {
		return History{}, err
	}
	for _, item := range series {
		history.Series = append(history.Series, HistorySeries{
			ID: item.ScopeID, Kind: "project", Name: item.Name,
			Points: aggregateScopes(item.Samples, history.From, step),
		})
	}
	return history, nil
}

func (application *Application) HostHistory(ctx context.Context, window time.Duration) (History, error) {
	return application.aggregateHistory(ctx, "host", "host", window)
}

func (application *Application) aggregateHistory(ctx context.Context, kind, id string, window time.Duration) (History, error) {
	history, step, err := application.historyWindow(window)
	if err != nil {
		return History{}, err
	}
	samples, err := application.store.AggregateMetricSamples(ctx, kind, id, history.From, history.To)
	if err != nil {
		return History{}, err
	}
	history.Points = aggregateScopes(samples, history.From, step)
	return history, nil
}

func (application *Application) historyWindow(window time.Duration) (History, time.Duration, error) {
	step, err := stepForWindow(window)
	if err != nil {
		return History{}, 0, err
	}
	to := application.now().UnixMilli()
	return History{
		From: to - window.Milliseconds(), To: to, StepMillis: step.Milliseconds(),
	}, step, nil
}

func (application *Application) Run(ctx context.Context, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}
	application.collect(ctx, onError)
	ticker := time.NewTicker(application.liveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			application.collect(ctx, onError)
		}
	}
}

func (application *Application) collect(ctx context.Context, onError func(error)) {
	targets, err := application.store.ResourceMetricTargets(ctx)
	if err != nil {
		onError(fmt.Errorf("list resource metric targets: %w", err))
		return
	}
	projectIDs, err := application.store.ResourceMetricProjectIDs(ctx)
	if err != nil {
		onError(fmt.Errorf("list resource metric projects: %w", err))
		return
	}
	publicNetwork, networkErr := application.network.PublicNetworkCounters()
	if networkErr != nil {
		onError(fmt.Errorf("read public network counters: %w", networkErr))
	}
	hostSample, hostErr := application.host.Read()
	if hostErr != nil {
		onError(fmt.Errorf("read host metrics: %w", hostErr))
	}
	diskSnapshot, diskErr := application.disk.Resources(ctx)
	if diskErr != nil {
		onError(fmt.Errorf("read resource disk usage: %w", diskErr))
	}
	diskReady := diskErr == nil && !diskSnapshot.CheckedAt.IsZero()
	diskByResource := make(map[metricKey]uint64, len(diskSnapshot.Resources))
	diskByProject := make(map[string]uint64, len(projectIDs))
	var installationDisk uint64
	if diskReady {
		for _, resource := range diskSnapshot.Resources {
			projectBytes := diskByProject[resource.ProjectID]
			if resource.Bytes > math.MaxInt64 || projectBytes > math.MaxInt64-resource.Bytes || installationDisk > math.MaxInt64-resource.Bytes {
				diskReady = false
				onError(errors.New("resource disk usage exceeds the metrics storage range"))
				break
			}
			key := metricKey{kind: resource.Kind, id: resource.ResourceID}
			diskByResource[key] = resource.Bytes
			diskByProject[resource.ProjectID] = projectBytes + resource.Bytes
			installationDisk += resource.Bytes
		}
	}

	application.mu.RLock()
	previous := application.previous
	previousHost := application.previousHost
	application.mu.RUnlock()
	collectedAt := application.now()
	var hostCurrent *HostCurrent
	var nextPreviousHost *previousHostSample
	if hostErr == nil {
		hostSample.ObservedAtMillis = collectedAt.UnixMilli()
		current := calculateHostCurrent(hostSample, previousHost, collectedAt)
		hostCurrent = &current
		nextPreviousHost = &previousHostSample{at: collectedAt, sample: hostSample}
	}

	resources := make(map[metricKey]Current, len(targets))
	activeResources := make(map[metricKey]struct{}, len(targets))
	nextPrevious := make(map[metricKey]previousSample, len(targets))
	projectBuilders := make(map[string]*aggregateBuilder, len(projectIDs))
	for _, projectID := range projectIDs {
		projectBuilders[projectID] = newAggregateBuilder()
	}
	installation := newAggregateBuilder()

	for _, target := range targets {
		kind := cgroupstats.Kind(target.Kind)
		key := metricKey{kind: target.Kind, id: target.ResourceID}
		trafficRoutes := TrafficRoutes{HTTP: target.HTTPRoute, TCP: target.TCPRoute, UDP: target.UDPRoute}
		activeResources[key] = struct{}{}
		usage, readErr := application.usage.Read(kind, target.ResourceID)
		if readErr != nil {
			onError(fmt.Errorf("read %s %s metrics: %w", target.Kind, target.ResourceID, readErr))
			builder := projectBuilder(projectBuilders, target.ProjectID)
			publicService := target.Kind == string(cgroupstats.Service)
			builder.missing(publicService, trafficRoutes)
			installation.missing(publicService, trafficRoutes)
			continue
		}
		usage.ObservedAtMillis = collectedAt.UnixMilli()
		current := Current{
			Sample: usage, TotalResources: 1,
			MemoryPeakBytes: max(usage.MemoryPeakBytes, usage.MemoryBytes),
		}
		if diskBytes, measured := diskByResource[key]; diskReady && measured {
			current.DiskBytes = uint64Pointer(diskBytes)
		}
		if usage.Running {
			current.RunningResources = 1
		}
		counters := publicNetwork.Counters[target.ResourceID]
		if target.Kind == string(cgroupstats.Service) {
			current.Proxy = proxyMetricsFromCounters(counters)
			current.TrafficRoutes = trafficRoutes
			if publicNetwork.Complete {
				current.NetworkRXBytes = counters.IngressBytes
				current.NetworkTXBytes = counters.EgressBytes
				current.NetworkAvailable = true
			}
		}
		if prior, exists := previous[key]; exists {
			calculateRates(&current, prior, counters, collectedAt)
		}
		resources[key] = current
		nextPrevious[key] = previousSample{
			at: collectedAt, cpuUsageMicros: usage.CPUUsageMicros,
			networkIngress: current.NetworkRXBytes, networkEgress: current.NetworkTXBytes,
			networkAvailable: current.NetworkAvailable, running: usage.Running,
			traffic: counters,
		}
		publicService := target.Kind == string(cgroupstats.Service)
		projectBuilder(projectBuilders, target.ProjectID).add(current, publicService)
		installation.add(current, publicService)
	}
	if diskReady {
		for _, diskResource := range diskSnapshot.Resources {
			key := metricKey{kind: diskResource.Kind, id: diskResource.ResourceID}
			if _, exists := resources[key]; exists {
				continue
			}
			current := Current{
				Sample: cgroupstats.Sample{
					ObservedAtMillis: collectedAt.UnixMilli(), HostCPUCores: hostSample.CPUCores,
					HostMemoryBytes: hostSample.MemoryTotalBytes,
				},
				DiskBytes: uint64Pointer(diskResource.Bytes), TotalResources: 1,
			}
			resources[key] = current
			activeResources[key] = struct{}{}
		}
	}

	projects := make(map[string]Current, len(projectBuilders))
	for projectID, builder := range projectBuilders {
		current := builder.finish()
		if diskReady {
			current.DiskBytes = uint64Pointer(diskByProject[projectID])
		}
		if current.ObservedAtMillis == 0 {
			current.ObservedAtMillis = collectedAt.UnixMilli()
		}
		projects[projectID] = current
	}
	installationCurrent := installation.finish()
	if diskReady {
		installationCurrent.DiskBytes = uint64Pointer(installationDisk)
	}
	if installationCurrent.ObservedAtMillis == 0 {
		installationCurrent.ObservedAtMillis = collectedAt.UnixMilli()
	}
	installationCurrent.Host = hostCurrent

	if application.rollup == nil {
		application.rollup = newMinuteAccumulator(collectedAt, resources, projects, installationCurrent)
	} else {
		application.rollup.add(collectedAt, activeResources, resources, projects, installationCurrent)
		if application.rollup.ready(collectedAt, application.persistInterval) {
			batch := application.rollup.batch(collectedAt, application.retention)
			if err := application.store.RecordMetricBatch(ctx, batch); err != nil {
				onError(fmt.Errorf("record metric batch: %w", err))
			} else {
				application.rollup = newMinuteAccumulator(collectedAt, resources, projects, installationCurrent)
			}
		}
	}

	application.mu.Lock()
	application.resources = resources
	application.projects = projects
	application.installation = installationCurrent
	application.ready = true
	application.previous = nextPrevious
	application.previousHost = nextPreviousHost
	application.mu.Unlock()
}

func projectBuilder(builders map[string]*aggregateBuilder, projectID string) *aggregateBuilder {
	builder := builders[projectID]
	if builder == nil {
		builder = newAggregateBuilder()
		builders[projectID] = builder
	}
	return builder
}
