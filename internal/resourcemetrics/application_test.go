package resourcemetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/diskusage"
	"github.com/iivankin/platformd/internal/hostmetrics"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type metricStoreStub struct {
	targets          []state.ResourceMetricTarget
	projects         []string
	samples          []state.ResourceMetricSample
	aggregateSamples []state.AggregateMetricSample
	resourceSeries   []state.ResourceMetricSeries
	projectSeries    []state.AggregateMetricSeries
	batches          []state.MetricBatch
	aggregateQueries int
}

func (store *metricStoreStub) ResourceMetricTargets(context.Context) ([]state.ResourceMetricTarget, error) {
	return store.targets, nil
}

func (store *metricStoreStub) ResourceMetricProjectIDs(context.Context) ([]string, error) {
	return store.projects, nil
}

func (store *metricStoreStub) RecordMetricBatch(_ context.Context, batch state.MetricBatch) error {
	store.batches = append(store.batches, batch)
	return nil
}

func (store *metricStoreStub) AggregateMetricSamples(context.Context, string, string, int64, int64) ([]state.AggregateMetricSample, error) {
	store.aggregateQueries++
	return store.aggregateSamples, nil
}

func (store *metricStoreStub) ResourceMetricSamples(context.Context, string, string, int64, int64) ([]state.ResourceMetricSample, error) {
	return store.samples, nil
}

func (store *metricStoreStub) ResourceMetricSeriesByProject(context.Context, string, int64, int64) ([]state.ResourceMetricSeries, error) {
	return store.resourceSeries, nil
}

func (store *metricStoreStub) ProjectAggregateMetricSeries(context.Context, int64, int64) ([]state.AggregateMetricSeries, error) {
	return store.projectSeries, nil
}

type usageReaderStub struct {
	sample cgroupstats.Sample
}

func (reader usageReaderStub) Read(cgroupstats.Kind, string) (cgroupstats.Sample, error) {
	return reader.sample, nil
}

type mappedUsageReader struct {
	samples map[metricKey]cgroupstats.Sample
	errors  map[metricKey]error
}

func (reader *mappedUsageReader) Read(kind cgroupstats.Kind, resourceID string) (cgroupstats.Sample, error) {
	key := metricKey{kind: string(kind), id: resourceID}
	return reader.samples[key], reader.errors[key]
}

type networkReaderStub struct {
	counters map[string]trafficmetrics.Counters
	complete bool
	err      error
}

func (reader networkReaderStub) PublicNetworkCounters() (PublicNetworkSnapshot, error) {
	return PublicNetworkSnapshot{Counters: reader.counters, Complete: reader.complete}, reader.err
}

type hostReaderStub struct {
	sample hostmetrics.Sample
}

type diskReaderStub struct {
	snapshot diskusage.ResourceSnapshot
}

func (reader diskReaderStub) Resources(context.Context) (diskusage.ResourceSnapshot, error) {
	return reader.snapshot, nil
}

func (reader hostReaderStub) Read() (hostmetrics.Sample, error) {
	return reader.sample, nil
}

func TestDiskUsageIncludesEveryProjectVolumeWithoutBlockingLiveTargets(t *testing.T) {
	store := &metricStoreStub{
		targets:  []state.ResourceMetricTarget{{Kind: "service", ResourceID: "api", ProjectID: "project"}},
		projects: []string{"project"},
	}
	disk := diskReaderStub{snapshot: diskusage.ResourceSnapshot{
		CheckedAt: time.Unix(99, 0),
		Resources: []diskusage.ResourceUsage{
			{Kind: "service", ResourceID: "api", ProjectID: "project", Bytes: 100},
			{Kind: "service", ResourceID: "disabled", ProjectID: "project", Bytes: 200},
		},
	}}
	application, err := NewApplication(
		store,
		usageReaderStub{sample: cgroupstats.Sample{Running: true}},
		networkReaderStub{complete: true}, disk, hostReaderStub{}, Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(err error) { t.Fatal(err) })
	resource, err := application.Read(cgroupstats.Service, "api")
	if err != nil || resource.DiskBytes == nil || *resource.DiskBytes != 100 {
		t.Fatalf("resource disk usage = %+v, %v", resource.DiskBytes, err)
	}
	disabled, err := application.Read(cgroupstats.Service, "disabled")
	if err != nil || disabled.DiskBytes == nil || *disabled.DiskBytes != 200 || disabled.Running {
		t.Fatalf("disk-only resource usage = %+v, %v", disabled, err)
	}
	project, err := application.ReadProject("project")
	if err != nil || project.DiskBytes == nil || *project.DiskBytes != 300 {
		t.Fatalf("project disk usage = %+v, %v", project.DiskBytes, err)
	}
	installation, err := application.ReadInstallation()
	if err != nil || installation.DiskBytes == nil || *installation.DiskBytes != 300 {
		t.Fatalf("installation disk usage = %+v, %v", installation.DiskBytes, err)
	}
}

func TestLiveSamplingUsesExactElapsedTimeAndTypedProtocolMetrics(t *testing.T) {
	clock := time.Unix(100, 0)
	store := &metricStoreStub{
		targets: []state.ResourceMetricTarget{
			{Kind: "service", ResourceID: "api", ProjectID: "project", HTTPRoute: true, UDPRoute: true},
			{Kind: "redis", ResourceID: "cache", ProjectID: "project"},
		},
		projects: []string{"project"},
	}
	usage := &mappedUsageReader{samples: map[metricKey]cgroupstats.Sample{
		{kind: "service", id: "api"}: {CPUUsageMicros: 1_000_000, MemoryBytes: 100, MemoryPeakBytes: 100, HostCPUCores: 8, HostMemoryBytes: 1000, Running: true},
		{kind: "redis", id: "cache"}: {CPUUsageMicros: 2_000_000, MemoryBytes: 200, HostCPUCores: 8, HostMemoryBytes: 1000, Running: true},
	}}
	initialTraffic := trafficmetrics.Counters{
		IngressBytes: 1000, EgressBytes: 500,
		HTTPRequestsTotal: 10, HTTPResponses2xxTotal: 8, HTTPResponses4xxTotal: 1, HTTPResponses5xxTotal: 1,
		TCPConnectionsTotal: 4, UDPIngressPackets: 20, UDPEgressPackets: 10,
	}
	initialTraffic.HTTPDurationBuckets[4] = 10
	network := &networkReaderStub{counters: map[string]trafficmetrics.Counters{"api": initialTraffic}, complete: true}
	host := &hostReaderStub{sample: hostmetrics.Sample{
		CPUUnits: 1_000, CPUIdleUnits: 400, CPUCores: 8,
		MemoryUsedBytes: 600, MemoryTotalBytes: 1_000,
		NetworkRXBytes: 1_000, NetworkTXBytes: 500, NetworkInterface: "eth0",
	}}
	application, err := NewApplication(store, usage, network, diskReaderStub{}, host, Config{
		LiveInterval: time.Second, PersistInterval: time.Minute,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(err error) { t.Fatalf("first collect: %v", err) })
	clock = clock.Add(2 * time.Second)
	usage.samples[metricKey{kind: "service", id: "api"}] = cgroupstats.Sample{CPUUsageMicros: 1_500_000, MemoryBytes: 110, MemoryPeakBytes: 175, HostCPUCores: 8, HostMemoryBytes: 1000, Running: true}
	usage.samples[metricKey{kind: "redis", id: "cache"}] = cgroupstats.Sample{CPUUsageMicros: 2_200_000, MemoryBytes: 210, HostCPUCores: 8, HostMemoryBytes: 1000, Running: true}
	nextTraffic := trafficmetrics.Counters{
		IngressBytes: 3000, EgressBytes: 1500,
		HTTPRequestsTotal: 14, HTTPRequestsPeakPerSecond: 4, HTTPActiveRequests: 2, HTTPActiveRequestsPeak: 5,
		HTTPResponses2xxTotal: 10, HTTPResponses4xxTotal: 2, HTTPResponses5xxTotal: 2,
		TCPConnectionsTotal: 6, TCPConnectionsPeakPerSecond: 3, TCPActiveConnections: 1, TCPActiveConnectionsPeak: 4,
		UDPIngressPackets: 26, UDPIngressPacketsPeakPerSecond: 5,
		UDPEgressPackets: 14, UDPEgressPacketsPeakPerSecond: 4,
		ProtocolRatePeaksAvailable: true,
	}
	nextTraffic.HTTPDurationBuckets[4] = 14
	network.counters["api"] = nextTraffic
	host.sample.CPUUnits, host.sample.CPUIdleUnits = 1_800, 600
	host.sample.MemoryUsedBytes = 650
	host.sample.NetworkRXBytes, host.sample.NetworkTXBytes = 5_000, 2_500
	application.collect(context.Background(), func(err error) { t.Fatalf("second collect: %v", err) })

	service, err := application.Read(cgroupstats.Service, "api")
	if err != nil || service.CPUMillicores == nil || *service.CPUMillicores != 250 ||
		service.NetworkIngressBytesPerSecond == nil || *service.NetworkIngressBytesPerSecond != 1000 ||
		service.MemoryPeakBytes != 175 || service.Proxy == nil || service.Proxy.HTTP.RequestsPerSecond == nil || *service.Proxy.HTTP.RequestsPerSecond != 2 ||
		service.Proxy.HTTP.RequestsPeakPerSecond == nil || *service.Proxy.HTTP.RequestsPeakPerSecond != 4 ||
		service.Proxy.HTTP.ActiveRequestsPeak != 5 ||
		service.Proxy.HTTP.LatencyP95Millis == nil || *service.Proxy.HTTP.LatencyP95Millis != 100 ||
		service.Proxy.TCP.ConnectionsPerSecond == nil || *service.Proxy.TCP.ConnectionsPerSecond != 1 ||
		service.Proxy.TCP.ConnectionsPeakPerSecond == nil || *service.Proxy.TCP.ConnectionsPeakPerSecond != 3 ||
		service.Proxy.UDP.IngressPacketsPerSecond == nil || *service.Proxy.UDP.IngressPacketsPerSecond != 3 ||
		!service.TrafficRoutes.HTTP || service.TrafficRoutes.TCP || !service.TrafficRoutes.UDP {
		t.Fatalf("service live usage = %+v, %v", service, err)
	}
	project, err := application.ReadProject("project")
	if err != nil || project.CPUMillicores == nil || *project.CPUMillicores != 350 ||
		project.MemoryBytes != 320 || project.RunningResources != 2 || project.TotalResources != 2 ||
		project.Proxy == nil || project.Proxy.HTTP.ActiveRequests != 2 || project.Proxy.HTTP.ActiveRequestsPeak != 5 ||
		project.Proxy.HTTP.RequestsPeakPerSecond == nil || *project.Proxy.HTTP.RequestsPeakPerSecond != 4 ||
		!project.TrafficRoutes.HTTP || project.TrafficRoutes.TCP || !project.TrafficRoutes.UDP {
		t.Fatalf("project live usage = %+v, %v", project, err)
	}
	installation, err := application.ReadInstallation()
	if err != nil || installation.Host == nil || installation.Host.CPUMillicores == nil ||
		*installation.Host.CPUMillicores != 6000 || installation.Host.NetworkIngressBytesPerSecond == nil ||
		*installation.Host.NetworkIngressBytesPerSecond != 2000 || !installation.TrafficRoutes.HTTP ||
		installation.TrafficRoutes.TCP || !installation.TrafficRoutes.UDP {
		t.Fatalf("installation live usage = %+v, %v", installation, err)
	}
}

func TestAggregateDoesNotSumPeaksFromDifferentServiceWindows(t *testing.T) {
	average, peak := 1.0, 10.0
	service := Current{Sample: cgroupstats.Sample{MemoryBytes: 100}, MemoryPeakBytes: 250, Proxy: &ProxyMetrics{
		HTTP: HTTPMetrics{
			RequestsPerSecond: &average, RequestsPeakPerSecond: &peak,
			Responses2xxPerSecond: &average, Responses3xxPerSecond: &average,
			Responses4xxPerSecond: &average, Responses5xxPerSecond: &average,
			ActiveRequests: 1, ActiveRequestsPeak: 10,
		},
		TCP: TCPMetrics{
			ConnectionsPerSecond: &average, ConnectionsPeakPerSecond: &peak,
			ActiveConnections: 1, ActiveConnectionsPeak: 10,
		},
		UDP: UDPMetrics{
			IngressPacketsPerSecond: &average, IngressPacketsPeakPerSecond: &peak,
			EgressPacketsPerSecond: &average, EgressPacketsPeakPerSecond: &peak,
		},
	}}
	builder := newAggregateBuilder()
	builder.add(service, true)
	builder.add(service, true)
	aggregate := builder.finish()
	if aggregate.MemoryBytes != 200 || aggregate.MemoryPeakBytes != 200 || aggregate.Proxy == nil || aggregate.Proxy.HTTP.RequestsPeakPerSecond == nil ||
		*aggregate.Proxy.HTTP.RequestsPeakPerSecond != 2 || aggregate.Proxy.HTTP.ActiveRequestsPeak != 2 ||
		aggregate.Proxy.TCP.ConnectionsPeakPerSecond == nil || *aggregate.Proxy.TCP.ConnectionsPeakPerSecond != 2 ||
		aggregate.Proxy.TCP.ActiveConnectionsPeak != 2 || aggregate.Proxy.UDP.IngressPacketsPeakPerSecond == nil ||
		*aggregate.Proxy.UDP.IngressPacketsPeakPerSecond != 2 {
		t.Fatalf("aggregate protocol peaks = %+v", aggregate.Proxy)
	}
}

func TestProxyRatesSurviveIncompleteNftablesSampleWithoutRecoverySpike(t *testing.T) {
	clock := time.Unix(200, 0)
	key := metricKey{kind: "service", id: "api"}
	store := &metricStoreStub{
		targets:  []state.ResourceMetricTarget{{Kind: "service", ResourceID: "api", ProjectID: "project"}},
		projects: []string{"project"},
	}
	usage := &mappedUsageReader{samples: map[metricKey]cgroupstats.Sample{
		key: {CPUUsageMicros: 1_000_000, MemoryBytes: 100, Running: true},
	}}
	network := &networkReaderStub{
		counters: map[string]trafficmetrics.Counters{"api": {HTTPRequestsTotal: 10, IngressBytes: 1_000}},
		complete: true,
	}
	application, err := NewApplication(store, usage, network, diskReaderStub{}, hostReaderStub{}, Config{
		LiveInterval: time.Second, PersistInterval: time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(err error) { t.Fatalf("initial collect: %v", err) })

	clock = clock.Add(2 * time.Second)
	network.counters["api"] = trafficmetrics.Counters{HTTPRequestsTotal: 12, IngressBytes: 1_200}
	network.complete = false
	network.err = errors.New("nftables unavailable")
	application.collect(context.Background(), func(error) {})
	current, err := application.Read(cgroupstats.Service, "api")
	if err != nil || current.Proxy == nil || current.Proxy.HTTP.RequestsPerSecond == nil ||
		*current.Proxy.HTTP.RequestsPerSecond != 1 || current.NetworkAvailable {
		t.Fatalf("incomplete network sample = %+v, %v", current, err)
	}

	clock = clock.Add(2 * time.Second)
	network.counters["api"] = trafficmetrics.Counters{HTTPRequestsTotal: 14, IngressBytes: 1_400}
	network.complete = true
	network.err = nil
	application.collect(context.Background(), func(err error) { t.Fatalf("recovery collect: %v", err) })
	current, err = application.Read(cgroupstats.Service, "api")
	if err != nil || current.Proxy == nil || current.Proxy.HTTP.RequestsPerSecond == nil ||
		*current.Proxy.HTTP.RequestsPerSecond != 1 || current.NetworkIngressBytesPerSecond != nil {
		t.Fatalf("recovered network sample = %+v, %v", current, err)
	}
}

func TestCollectKeepsActiveResourceRollupAcrossCgroupReadFailure(t *testing.T) {
	clock := time.Unix(250, 0)
	key := metricKey{kind: "service", id: "api"}
	store := &metricStoreStub{
		targets:  []state.ResourceMetricTarget{{Kind: "service", ResourceID: "api", ProjectID: "project"}},
		projects: []string{"project"},
	}
	usage := &mappedUsageReader{
		samples: map[metricKey]cgroupstats.Sample{
			key: {CPUUsageMicros: 1_000_000, MemoryBytes: 100, Running: true},
		},
		errors: make(map[metricKey]error),
	}
	application, err := NewApplication(store, usage, networkReaderStub{complete: true}, diskReaderStub{}, hostReaderStub{}, Config{
		LiveInterval: time.Second, PersistInterval: time.Minute, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(err error) { t.Fatalf("initial collect: %v", err) })

	clock = clock.Add(2 * time.Second)
	usage.samples[key] = cgroupstats.Sample{CPUUsageMicros: 1_200_000, MemoryBytes: 110, Running: true}
	application.collect(context.Background(), func(err error) { t.Fatalf("second collect: %v", err) })

	clock = clock.Add(2 * time.Second)
	usage.errors[key] = errors.New("cgroup temporarily unavailable")
	application.collect(context.Background(), func(error) {})

	clock = clock.Add(2 * time.Second)
	delete(usage.errors, key)
	usage.samples[key] = cgroupstats.Sample{CPUUsageMicros: 1_400_000, MemoryBytes: 120, Running: true}
	application.collect(context.Background(), func(err error) { t.Fatalf("recovery collect: %v", err) })

	batch := application.rollup.batch(clock, Retention)
	if len(batch.Resources) != 1 || batch.Resources[0].DurationMillis != 4_000 ||
		batch.Resources[0].CPUDurationMillis != 2_000 {
		t.Fatalf("resource rollup after cgroup recovery = %+v", batch.Resources)
	}
}

func TestMinuteRollupUsesCounterDeltasAndKeepsTwoSecondPeak(t *testing.T) {
	start := time.Unix(100, 0)
	key := metricKey{kind: "service", id: "api"}
	seed := Current{Sample: cgroupstats.Sample{ObservedAtMillis: start.UnixMilli(), MemoryBytes: 100, Running: true}, MemoryPeakBytes: 100}
	accumulator := newMinuteAccumulator(start, map[metricKey]Current{key: seed}, nil, Current{})
	current := seed
	var requestsTotal uint64
	for index, delta := range []uint64{2, 40, 0} {
		current.ObservedAtMillis = start.Add(time.Duration(index+1) * 2 * time.Second).UnixMilli()
		current.MemoryBytes = uint64(110 + index*10)
		current.MemoryPeakBytes = current.MemoryBytes
		if index == 1 {
			current.MemoryPeakBytes = 500
		}
		traffic := &trafficDelta{httpRequests: delta}
		traffic.httpDurationBuckets[4] = delta
		current.interval = metricInterval{duration: 2 * time.Second, cpuUsageMicros: uint64Pointer(delta * 1000), networkIngressBytes: uint64Pointer(delta * 1000), networkEgressBytes: uint64Pointer(delta * 500), traffic: traffic}
		cpu := int64(delta) / 2
		ingress, egress := int64(delta*500), int64(delta*250)
		requestRate, zeroRate := float64(delta)/2, float64(0)
		requestsTotal += delta
		current.Proxy = &ProxyMetrics{
			HTTP: HTTPMetrics{RequestsPerSecond: &requestRate, RequestsPeakPerSecond: &requestRate, RequestsTotal: requestsTotal},
			TCP:  TCPMetrics{ConnectionsPerSecond: &zeroRate, ConnectionsPeakPerSecond: &zeroRate},
			UDP:  UDPMetrics{IngressPacketsPerSecond: &zeroRate, IngressPacketsPeakPerSecond: &zeroRate, EgressPacketsPerSecond: &zeroRate, EgressPacketsPeakPerSecond: &zeroRate},
		}
		current.CPUMillicores, current.CPUPeakMillicores = int64Pointer(cpu), int64Pointer(cpu)
		current.NetworkIngressBytesPerSecond, current.NetworkIngressPeakBytesPerSecond = int64Pointer(ingress), int64Pointer(ingress)
		current.NetworkEgressBytesPerSecond, current.NetworkEgressPeakBytesPerSecond = int64Pointer(egress), int64Pointer(egress)
		accumulator.add(
			start.Add(time.Duration(index+1)*2*time.Second), map[metricKey]struct{}{key: {}},
			map[metricKey]Current{key: current}, nil, Current{},
		)
	}
	batch := accumulator.batch(start.Add(6*time.Second), Retention)
	if len(batch.Resources) != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	sample := batch.Resources[0]
	if sample.CPUMillicores == nil || *sample.CPUMillicores != 7 || sample.CPUPeakMillicores == nil || *sample.CPUPeakMillicores != 20 ||
		sample.CPUDurationMillis != 6_000 || sample.NetworkDurationMillis != 6_000 || sample.ProxyDurationMillis != 6_000 ||
		sample.NetworkIngressBytesPerSecond == nil || *sample.NetworkIngressBytesPerSecond != 7000 ||
		sample.NetworkIngressPeakBytesPerSecond == nil || *sample.NetworkIngressPeakBytesPerSecond != 20_000 ||
		sample.MemoryBytes != 120 || sample.MemoryPeakBytes != 500 || sample.Proxy == nil ||
		sample.Proxy.HTTP.RequestsPerSecond == nil || *sample.Proxy.HTTP.RequestsPerSecond != 7 ||
		sample.Proxy.HTTP.RequestsPeakPerSecond == nil || *sample.Proxy.HTTP.RequestsPeakPerSecond != 20 ||
		sample.Proxy.HTTP.DurationBuckets[4] != 42 {
		t.Fatalf("rolled sample = %+v", sample)
	}
}

func TestMinuteRollupPersistsProjectAndHostWithoutInstallationHistory(t *testing.T) {
	start := time.Unix(200, 0)
	project := Current{
		Sample:          cgroupstats.Sample{MemoryBytes: 100, Running: true},
		MemoryPeakBytes: 100, RunningResources: 1, TotalResources: 1,
	}
	host := HostCurrent{MemoryUsedBytes: 200, MemoryPeakBytes: 200, MemoryTotalBytes: 1000}
	installation := Current{Host: &host}
	accumulator := newMinuteAccumulator(
		start, nil, map[string]Current{"project": project}, installation,
	)
	project.interval = metricInterval{duration: 2 * time.Second}
	host.interval = metricInterval{duration: 2 * time.Second}
	installation.Host = &host
	accumulator.add(
		start.Add(2*time.Second), nil, nil, map[string]Current{"project": project}, installation,
	)

	batch := accumulator.batch(start.Add(2*time.Second), Retention)
	scopes := make(map[string]bool, len(batch.Aggregates))
	for _, sample := range batch.Aggregates {
		scopes[sample.ScopeKind] = true
	}
	if len(batch.Aggregates) != 2 || !scopes["project"] || !scopes["host"] || scopes["installation"] {
		t.Fatalf("aggregate metric scopes = %+v", batch.Aggregates)
	}
}

func TestMinuteRollupKeepsActiveResourceAcrossTransientReadGap(t *testing.T) {
	start := time.Unix(300, 0)
	key := metricKey{kind: "service", id: "api"}
	seed := Current{Sample: cgroupstats.Sample{ObservedAtMillis: start.UnixMilli(), MemoryBytes: 100, Running: true}, MemoryPeakBytes: 100}
	accumulator := newMinuteAccumulator(start, map[metricKey]Current{key: seed}, nil, Current{})
	active := map[metricKey]struct{}{key: {}}
	cpu := int64(100)
	current := seed
	current.CPUMillicores, current.CPUPeakMillicores = &cpu, &cpu
	current.interval = metricInterval{duration: 2 * time.Second, cpuUsageMicros: uint64Pointer(200)}
	accumulator.add(start.Add(2*time.Second), active, map[metricKey]Current{key: current}, nil, Current{})
	accumulator.add(start.Add(4*time.Second), active, map[metricKey]Current{}, nil, Current{})
	accumulator.add(start.Add(6*time.Second), active, map[metricKey]Current{key: current}, nil, Current{})

	batch := accumulator.batch(start.Add(6*time.Second), Retention)
	if len(batch.Resources) != 1 || batch.Resources[0].DurationMillis != 4_000 ||
		batch.Resources[0].CPUDurationMillis != 4_000 {
		t.Fatalf("rollup after transient gap = %+v", batch.Resources)
	}
	accumulator.add(start.Add(8*time.Second), map[metricKey]struct{}{}, map[metricKey]Current{}, nil, Current{})
	if batch = accumulator.batch(start.Add(8*time.Second), Retention); len(batch.Resources) != 0 {
		t.Fatalf("deleted resource remained in rollup: %+v", batch.Resources)
	}
}

func TestHistoryWeightsMinuteAveragesAndMergesLatencyHistograms(t *testing.T) {
	from := int64(3_600_000)
	averageOne, peakOne := int64(10), int64(100)
	averageTwo, peakTwo := int64(30), int64(40)
	egressOne, egressPeakOne := int64(20), int64(120)
	egressTwo, egressPeakTwo := int64(40), int64(50)
	diskOne, diskTwo := uint64(100), uint64(400)
	firstProxy, secondProxy := validProxySample(), validProxySample()
	*firstProxy.HTTP.RequestsPerSecond, *firstProxy.HTTP.RequestsPeakPerSecond = 10, 10
	*secondProxy.HTTP.RequestsPerSecond, *secondProxy.HTTP.RequestsPeakPerSecond = 30, 30
	firstProxy.HTTP.DurationBuckets[0], firstProxy.HTTP.DurationBuckets[5] = 95, 5
	secondProxy.HTTP.DurationBuckets[0] = 100
	points := aggregateResources([]state.ResourceMetricSample{
		{Kind: "service", ResourceID: "api", ObservedAt: from, MetricValues: state.MetricValues{
			DurationMillis: 60_000, CPUDurationMillis: 30_000, NetworkDurationMillis: 30_000, ProxyDurationMillis: 30_000,
			CPUMillicores: &averageOne, CPUPeakMillicores: &peakOne,
			MemoryBytes: 100, MemoryPeakBytes: 150, DiskBytes: &diskOne,
			NetworkIngressBytesPerSecond: &averageOne, NetworkIngressPeakBytesPerSecond: &peakOne,
			NetworkEgressBytesPerSecond: &egressOne, NetworkEgressPeakBytesPerSecond: &egressPeakOne,
			Proxy: firstProxy,
		}},
		{Kind: "service", ResourceID: "api", ObservedAt: from + 60_000, MetricValues: state.MetricValues{
			DurationMillis: 120_000, CPUDurationMillis: 120_000, NetworkDurationMillis: 120_000, ProxyDurationMillis: 120_000,
			CPUMillicores: &averageTwo, CPUPeakMillicores: &peakTwo,
			MemoryBytes: 400, MemoryPeakBytes: 450, DiskBytes: &diskTwo,
			NetworkIngressBytesPerSecond: &averageTwo, NetworkIngressPeakBytesPerSecond: &peakTwo,
			NetworkEgressBytesPerSecond: &egressTwo, NetworkEgressPeakBytesPerSecond: &egressPeakTwo,
			Proxy: secondProxy,
		}},
	}, from, 5*time.Minute)
	if len(points) != 1 || points[0].CPUMillicores == nil || *points[0].CPUMillicores != 26 ||
		points[0].CPUPeakMillicores == nil || *points[0].CPUPeakMillicores != 100 || points[0].Proxy == nil ||
		points[0].Proxy.HTTP.RequestsPerSecond == nil || *points[0].Proxy.HTTP.RequestsPerSecond != 26 ||
		points[0].MemoryBytes != 300 || points[0].MemoryPeakBytes != 450 ||
		points[0].DiskBytes == nil || *points[0].DiskBytes != 300 ||
		points[0].NetworkIngressBytesPerSecond == nil || *points[0].NetworkIngressBytesPerSecond != 26 ||
		points[0].NetworkEgressBytesPerSecond == nil || *points[0].NetworkEgressBytesPerSecond != 36 ||
		points[0].Proxy.HTTP.LatencyP95Millis == nil || *points[0].Proxy.HTTP.LatencyP95Millis != 5 {
		t.Fatalf("history points = %+v", points)
	}
}

func TestAggregateHistoryIncludesWorkloadAndProjectSeries(t *testing.T) {
	observedAt := time.Unix(7200, 0).UnixMilli()
	diskBytes := uint64(4096)
	values := state.MetricValues{
		DurationMillis: 60_000, MemoryBytes: 100, MemoryPeakBytes: 100, DiskBytes: &diskBytes,
	}
	store := &metricStoreStub{
		aggregateSamples: []state.AggregateMetricSample{{
			ScopeKind: "project", ScopeID: "project", ObservedAt: observedAt,
			RunningResources: 1, TotalResources: 1, MetricValues: values,
		}},
		resourceSeries: []state.ResourceMetricSeries{{
			Kind: "service", ResourceID: "api", Name: "api",
			Samples: []state.ResourceMetricSample{{
				Kind: "service", ResourceID: "api", ObservedAt: observedAt, MetricValues: values,
			}},
		}},
		projectSeries: []state.AggregateMetricSeries{{
			ScopeID: "project", Name: "production",
			Samples: []state.AggregateMetricSample{{
				ScopeKind: "project", ScopeID: "project", ObservedAt: observedAt,
				RunningResources: 1, TotalResources: 1, MetricValues: values,
			}},
		}},
	}
	application, err := NewApplication(
		store, usageReaderStub{}, networkReaderStub{}, diskReaderStub{}, hostReaderStub{},
		Config{Now: func() time.Time { return time.Unix(7260, 0) }},
	)
	if err != nil {
		t.Fatal(err)
	}
	project, err := application.ProjectHistory(context.Background(), "project", time.Hour)
	if err != nil || len(project.Series) != 1 || project.Series[0].Name != "api" ||
		len(project.Series[0].Points) != 1 || project.Series[0].Points[0].DiskBytes == nil || len(project.Points) != 0 {
		t.Fatalf("project history series = %+v, %v", project.Series, err)
	}
	installation, err := application.InstallationHistory(context.Background(), time.Hour)
	if err != nil || len(installation.Series) != 1 || installation.Series[0].Kind != "project" ||
		installation.Series[0].Name != "production" || len(installation.Points) != 0 {
		t.Fatalf("installation history series = %+v, %v", installation.Series, err)
	}
	if store.aggregateQueries != 0 {
		t.Fatalf("aggregate total queries = %d, want 0", store.aggregateQueries)
	}
}

func TestHistoryWindowUsesDashboardBucketDensity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		window  time.Duration
		buckets int
	}{
		{window: time.Hour, buckets: 60},
		{window: 6 * time.Hour, buckets: 72},
		{window: 24 * time.Hour, buckets: 96},
		{window: 7 * 24 * time.Hour, buckets: 168},
		{window: 30 * 24 * time.Hour, buckets: 120},
	}
	for _, test := range tests {
		step, err := stepForWindow(test.window)
		if err != nil {
			t.Fatalf("step for %s: %v", test.window, err)
		}
		if got := int(test.window / step); got != test.buckets {
			t.Fatalf("buckets for %s = %d, want %d", test.window, got, test.buckets)
		}
	}
}

func validProxySample() *state.ProxyMetricSample {
	average, peak := 1.0, 2.0
	sample := &state.ProxyMetricSample{}
	sample.HTTP.RequestsPerSecond, sample.HTTP.RequestsPeakPerSecond = &average, &peak
	sample.TCP.ConnectionsPerSecond, sample.TCP.ConnectionsPeakPerSecond = &average, &peak
	sample.UDP.IngressPacketsPerSecond, sample.UDP.IngressPacketsPeakPerSecond = &average, &peak
	sample.UDP.EgressPacketsPerSecond, sample.UDP.EgressPacketsPeakPerSecond = &average, &peak
	return sample
}
