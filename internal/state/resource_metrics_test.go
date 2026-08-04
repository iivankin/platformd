package state_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestMetricSeriesQueriesGroupResourcesAndProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "production", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "actor@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, state.CreateService{
		ID: "api", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine")},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "actor@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	diskBytes := uint64(2048)
	values := state.MetricValues{DurationMillis: 60_000, MemoryBytes: 10, MemoryPeakBytes: 10, DiskBytes: &diskBytes}
	if err := store.RecordMetricBatch(ctx, state.MetricBatch{
		Resources: []state.ResourceMetricSample{{
			Kind: "service", ResourceID: "api", ObservedAt: 100, MetricValues: values,
		}},
		Aggregates: []state.AggregateMetricSample{{
			ScopeKind: "project", ScopeID: "project", ObservedAt: 100,
			RunningResources: 1, TotalResources: 1, MetricValues: values,
		}},
		RetentionCutoff: 1,
	}); err != nil {
		t.Fatal(err)
	}
	resources, err := store.ResourceMetricSeriesByProject(ctx, "project", 1, 200)
	if err != nil || len(resources) != 1 || resources[0].Name != "api" ||
		len(resources[0].Samples) != 1 || resources[0].Samples[0].DiskBytes == nil {
		t.Fatalf("resource metric series = %+v, %v", resources, err)
	}
	projects, err := store.ProjectAggregateMetricSeries(ctx, 1, 200)
	if err != nil || len(projects) != 1 || projects[0].Name != "production" ||
		len(projects[0].Samples) != 1 || projects[0].Samples[0].DiskBytes == nil {
		t.Fatalf("project metric series = %+v, %v", projects, err)
	}
}

func TestMetricBatchPersistsRollupsAndAppliesRetention(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	cpu, cpuPeak := int64(250), int64(900)
	ingress, ingressPeak := int64(1000), int64(5000)
	egress, egressPeak := int64(500), int64(2000)
	rps, rpsPeak := 4.5, 30.0
	diskBytes := uint64(8192)
	proxy := &state.ProxyMetricSample{}
	proxy.HTTP.RequestsPerSecond = &rps
	proxy.HTTP.RequestsPeakPerSecond = &rpsPeak
	proxy.HTTP.RequestsTotal = 270
	proxy.HTTP.ActiveRequests = 2
	proxy.HTTP.ActiveRequestsPeak = 8
	proxy.HTTP.DurationBuckets[3] = 270

	first := state.ResourceMetricSample{
		Kind: "service", ResourceID: "api", ObservedAt: 100,
		MetricValues: state.MetricValues{
			DurationMillis: 60_000, CPUDurationMillis: 45_000, NetworkDurationMillis: 30_000, ProxyDurationMillis: 15_000,
			CPUMillicores: &cpu, CPUPeakMillicores: &cpuPeak,
			MemoryBytes: 100, MemoryPeakBytes: 160,
			DiskBytes:                    &diskBytes,
			NetworkIngressBytesPerSecond: &ingress, NetworkIngressPeakBytesPerSecond: &ingressPeak,
			NetworkEgressBytesPerSecond: &egress, NetworkEgressPeakBytesPerSecond: &egressPeak,
			Running: true, Proxy: proxy,
		},
	}
	if err := store.RecordMetricBatch(context.Background(), state.MetricBatch{
		Resources: []state.ResourceMetricSample{first}, RetentionCutoff: 1,
	}); err != nil {
		t.Fatal(err)
	}
	window, err := store.ResourceMetricSamples(context.Background(), "service", "api", 1, 200)
	if err != nil || len(window) != 1 || window[0].CPUDurationMillis != 45_000 ||
		window[0].NetworkDurationMillis != 30_000 || window[0].ProxyDurationMillis != 15_000 ||
		window[0].CPUPeakMillicores == nil || *window[0].CPUPeakMillicores != cpuPeak ||
		window[0].MemoryPeakBytes != 160 || window[0].DiskBytes == nil || *window[0].DiskBytes != diskBytes ||
		window[0].NetworkIngressPeakBytesPerSecond == nil ||
		*window[0].NetworkIngressPeakBytesPerSecond != ingressPeak || window[0].Proxy == nil ||
		window[0].Proxy.HTTP.RequestsPeakPerSecond == nil || *window[0].Proxy.HTTP.RequestsPeakPerSecond != rpsPeak ||
		window[0].Proxy.HTTP.DurationBuckets[3] != 270 {
		t.Fatalf("metric window = %+v, %v", window, err)
	}
	if err := store.RecordMetricBatch(context.Background(), state.MetricBatch{RetentionCutoff: 150}); err != nil {
		t.Fatal(err)
	}
	retained, err := store.ResourceMetricSamples(context.Background(), "service", "api", 1, 200)
	if err != nil || len(retained) != 0 {
		t.Fatalf("retained metrics = %+v, %v", retained, err)
	}
}

func TestMetricBatchPersistsProjectInstallationAndHostTogether(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	cpu, cpuPeak := int64(320), int64(700)
	ingress, ingressPeak := int64(2048), int64(8192)
	egress, egressPeak := int64(1024), int64(4096)
	rps, rpsPeak := 12.5, 40.0
	diskBytes := uint64(65_536)
	projectProxy := &state.ProxyMetricSample{}
	projectProxy.HTTP.RequestsPerSecond = &rps
	projectProxy.HTTP.RequestsPeakPerSecond = &rpsPeak
	projectProxy.HTTP.RequestsTotal = 100
	projectProxy.TCP.ActiveConnections = 2
	projectProxy.TCP.ActiveConnectionsPeak = 4
	values := state.MetricValues{
		DurationMillis: 60_000, CPUDurationMillis: 45_000, NetworkDurationMillis: 30_000, ProxyDurationMillis: 15_000,
		CPUMillicores: &cpu, CPUPeakMillicores: &cpuPeak,
		MemoryBytes: 4096, MemoryPeakBytes: 5000,
		DiskBytes:                    &diskBytes,
		NetworkIngressBytesPerSecond: &ingress, NetworkIngressPeakBytesPerSecond: &ingressPeak,
		NetworkEgressBytesPerSecond: &egress, NetworkEgressPeakBytesPerSecond: &egressPeak,
		Proxy: projectProxy,
	}
	samples := []state.AggregateMetricSample{
		{ScopeKind: "project", ScopeID: "project", ObservedAt: 100, RunningResources: 2, TotalResources: 3, MetricValues: values},
		{ScopeKind: "installation", ScopeID: "installation", ObservedAt: 100, TotalResources: 3, MetricValues: values},
		{ScopeKind: "host", ScopeID: "host", ObservedAt: 100, RunningResources: 1, TotalResources: 1, MetricValues: values},
	}
	if err := store.RecordMetricBatch(context.Background(), state.MetricBatch{Aggregates: samples, RetentionCutoff: 1}); err != nil {
		t.Fatal(err)
	}
	project, err := store.AggregateMetricSamples(context.Background(), "project", "project", 1, 200)
	if err != nil || len(project) != 1 || project[0].CPUDurationMillis != 45_000 ||
		project[0].NetworkDurationMillis != 30_000 || project[0].ProxyDurationMillis != 15_000 ||
		project[0].CPUMillicores == nil || *project[0].CPUMillicores != cpu ||
		project[0].CPUPeakMillicores == nil || *project[0].CPUPeakMillicores != cpuPeak ||
		project[0].RunningResources != 2 || project[0].DiskBytes == nil || *project[0].DiskBytes != diskBytes ||
		project[0].Proxy == nil ||
		project[0].Proxy.HTTP.RequestsPerSecond == nil || *project[0].Proxy.HTTP.RequestsPerSecond != rps ||
		project[0].Proxy.TCP.ActiveConnectionsPeak != 4 {
		t.Fatalf("project aggregate metrics = %+v, %v", project, err)
	}
	host, err := store.AggregateMetricSamples(context.Background(), "host", "host", 1, 200)
	if err != nil || len(host) != 1 || host[0].MemoryPeakBytes != 5000 ||
		host[0].NetworkEgressPeakBytesPerSecond == nil || *host[0].NetworkEgressPeakBytesPerSecond != egressPeak {
		t.Fatalf("host aggregate metrics = %+v, %v", host, err)
	}
}

func TestMetricBatchRejectsWholeInvalidBatch(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	valid := state.ResourceMetricSample{
		Kind: "service", ResourceID: "api", ObservedAt: 100,
		MetricValues: state.MetricValues{DurationMillis: 60_000, MemoryBytes: 10, MemoryPeakBytes: 10},
	}
	invalid := state.AggregateMetricSample{
		ScopeKind: "project", ScopeID: "project", ObservedAt: 100,
		MetricValues: state.MetricValues{DurationMillis: 60_000, MemoryBytes: 20, MemoryPeakBytes: 10},
	}
	if err := store.RecordMetricBatch(context.Background(), state.MetricBatch{
		Resources: []state.ResourceMetricSample{valid}, Aggregates: []state.AggregateMetricSample{invalid}, RetentionCutoff: 1,
	}); err == nil {
		t.Fatal("expected invalid batch to fail")
	}
	got, err := store.ResourceMetricSamples(context.Background(), "service", "api", 1, 200)
	if err != nil || len(got) != 0 {
		t.Fatalf("partial metric batch was committed: %+v, %v", got, err)
	}
}
