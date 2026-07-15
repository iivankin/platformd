package resourcemetrics

import (
	"context"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/state"
)

type metricStoreStub struct {
	targets  []state.ResourceMetricTarget
	samples  []state.ResourceMetricSample
	recorded []state.ResourceMetricSample
	cutoff   int64
}

func (store *metricStoreStub) ResourceMetricTargets(context.Context) ([]state.ResourceMetricTarget, error) {
	return store.targets, nil
}

func (store *metricStoreStub) RecordResourceMetricSamples(_ context.Context, samples []state.ResourceMetricSample) error {
	store.recorded = append(store.recorded, samples...)
	return nil
}

func (store *metricStoreStub) ResourceMetricSamples(context.Context, string, string, int64, int64) ([]state.ResourceMetricSample, error) {
	return store.samples, nil
}

func (store *metricStoreStub) DeleteResourceMetricSamplesBefore(_ context.Context, cutoff int64) error {
	store.cutoff = cutoff
	return nil
}

type usageReaderStub struct {
	sample cgroupstats.Sample
}

func (reader usageReaderStub) Read(cgroupstats.Kind, string) (cgroupstats.Sample, error) {
	return reader.sample, nil
}

type networkReaderStub struct {
	counters  NetworkCounters
	available bool
}

func (reader networkReaderStub) ReadResourceNetwork(cgroupstats.Kind, string) (NetworkCounters, bool, error) {
	return reader.counters, reader.available, nil
}

func TestHistoryCalculatesRatesAndLeavesCounterResetsEmpty(t *testing.T) {
	from := int64(3_600_000)
	store := &metricStoreStub{samples: []state.ResourceMetricSample{
		metricSample(from-60_000, 100_000, 100, 1_000, 2_000),
		metricSample(from, 160_000, 200, 7_000, 5_000),
		metricSample(from+60_000, 10_000, 300, 100, 100),
	}}
	application, err := NewApplication(store, usageReaderStub{}, networkReaderStub{}, Config{
		Now: func() time.Time { return time.UnixMilli(from + time.Hour.Milliseconds()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := application.History(context.Background(), cgroupstats.Service, "api", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Points) != 2 || history.StepMillis != time.Minute.Milliseconds() {
		t.Fatalf("history = %+v", history)
	}
	first := history.Points[0]
	if first.CPUMillicores == nil || *first.CPUMillicores != 1 ||
		first.NetworkIngressBytesPerSecond == nil || *first.NetworkIngressBytesPerSecond != 100 ||
		first.NetworkEgressBytesPerSecond == nil || *first.NetworkEgressBytesPerSecond != 50 {
		t.Fatalf("first point = %+v", first)
	}
	second := history.Points[1]
	if second.CPUMillicores != nil || second.NetworkIngressBytesPerSecond != nil || second.NetworkEgressBytesPerSecond != nil {
		t.Fatalf("reset point = %+v", second)
	}
}

func TestCollectStoresNetworkCountersAndAppliesRetention(t *testing.T) {
	now := time.UnixMilli(40 * 24 * time.Hour.Milliseconds())
	store := &metricStoreStub{targets: []state.ResourceMetricTarget{{Kind: "redis", ResourceID: "cache"}}}
	application, err := NewApplication(store, usageReaderStub{sample: cgroupstats.Sample{
		ObservedAtMillis: now.UnixMilli(), CPUUsageMicros: 42, MemoryBytes: 84, Running: true,
	}}, networkReaderStub{counters: NetworkCounters{RXBytes: 126, TXBytes: 168}, available: true}, Config{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	application.collect(context.Background(), func(err error) { t.Fatalf("collect: %v", err) })
	if len(store.recorded) != 1 || store.recorded[0].NetworkRXBytes == nil || *store.recorded[0].NetworkRXBytes != 126 ||
		store.recorded[0].NetworkTXBytes == nil || *store.recorded[0].NetworkTXBytes != 168 {
		t.Fatalf("recorded = %+v", store.recorded)
	}
	if want := now.Add(-Retention).UnixMilli(); store.cutoff != want {
		t.Fatalf("cutoff = %d, want %d", store.cutoff, want)
	}
}

func TestHistoryDoesNotBridgeCollectorGaps(t *testing.T) {
	from := int64(3_600_000)
	points := aggregate([]state.ResourceMetricSample{
		metricSample(from-10*time.Minute.Milliseconds(), 100, 100, 100, 100),
		metricSample(from, 1_000, 200, 1_000, 1_000),
	}, from, time.Minute)
	if len(points) != 1 || points[0].CPUMillicores != nil ||
		points[0].NetworkIngressBytesPerSecond != nil || points[0].NetworkEgressBytesPerSecond != nil {
		t.Fatalf("gap point = %+v", points)
	}
}

func metricSample(observedAt int64, cpu, memory, rx, tx uint64) state.ResourceMetricSample {
	return state.ResourceMetricSample{
		Kind: "service", ResourceID: "api", ObservedAt: observedAt,
		CPUUsageMicros: cpu, MemoryBytes: memory,
		NetworkRXBytes: &rx, NetworkTXBytes: &tx, Running: true,
	}
}
