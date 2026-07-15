package state_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestResourceMetricSamplesPreservePreviousCounterAndApplyRetention(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	defer store.Close()
	rx, tx := uint64(1000), uint64(2000)
	samples := []state.ResourceMetricSample{
		{Kind: "service", ResourceID: "api", ObservedAt: 100, CPUUsageMicros: 1000, MemoryBytes: 10, NetworkRXBytes: &rx, NetworkTXBytes: &tx, Running: true},
		{Kind: "service", ResourceID: "api", ObservedAt: 200, CPUUsageMicros: 1500, MemoryBytes: 20, NetworkRXBytes: pointerTo(uint64(1300)), NetworkTXBytes: pointerTo(uint64(2600)), Running: true},
		{Kind: "service", ResourceID: "api", ObservedAt: 300, CPUUsageMicros: 2000, MemoryBytes: 30, Running: false},
	}
	if err := store.RecordResourceMetricSamples(context.Background(), samples); err != nil {
		t.Fatal(err)
	}
	window, err := store.ResourceMetricSamples(context.Background(), "service", "api", 150, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 3 || window[0].ObservedAt != 100 || window[1].NetworkRXBytes == nil || *window[1].NetworkRXBytes != 1300 || window[2].Running {
		t.Fatalf("metric window = %+v", window)
	}
	if err := store.DeleteResourceMetricSamplesBefore(context.Background(), 250); err != nil {
		t.Fatal(err)
	}
	retained, err := store.ResourceMetricSamples(context.Background(), "service", "api", 250, 350)
	if err != nil || len(retained) != 1 || retained[0].ObservedAt != 300 {
		t.Fatalf("retained metrics = %+v, %v", retained, err)
	}
}

func pointerTo[T any](value T) *T {
	return &value
}
