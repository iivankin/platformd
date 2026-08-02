package trafficmetrics

import (
	"sync"
	"testing"
	"time"
)

func TestCollectCapturesExactActiveAndOneSecondProtocolPeaks(t *testing.T) {
	registry := NewRegistry()
	registry.StartHTTP("service")
	registry.FinishHTTP("service", 200, time.Millisecond)
	startedAt := time.Unix(100, 0)
	registry.sampleProtocolRates(startedAt)

	registry.StartHTTP("service")
	registry.StartHTTP("service")
	registry.FinishHTTP("service", 200, time.Millisecond)
	registry.FinishHTTP("service", 200, time.Millisecond)
	registry.StartTCP("service")
	registry.FinishTCP("service")
	registry.AddUDPIngress("service", 10)
	registry.AddUDPIngress("service", 10)
	registry.AddUDPIngress("service", 10)
	registry.AddUDPEgress("service", 10)
	registry.sampleProtocolRates(startedAt.Add(time.Second))

	counters := registry.Collect()["service"]
	if counters.HTTPActiveRequests != 0 || counters.HTTPActiveRequestsPeak != 2 ||
		counters.TCPActiveConnections != 0 || counters.TCPActiveConnectionsPeak != 1 {
		t.Fatalf("active peaks = %+v", counters)
	}
	if !counters.ProtocolRatePeaksAvailable || counters.HTTPRequestsPeakPerSecond != 2 ||
		counters.TCPConnectionsPeakPerSecond != 1 || counters.UDPIngressPacketsPeakPerSecond != 3 ||
		counters.UDPEgressPacketsPeakPerSecond != 1 {
		t.Fatalf("protocol rate peaks = %+v", counters)
	}

	next := registry.Collect()["service"]
	if next.HTTPActiveRequestsPeak != 0 || next.TCPActiveConnectionsPeak != 0 || next.ProtocolRatePeaksAvailable {
		t.Fatalf("peak interval was not reset: %+v", next)
	}
}

func TestCollectKeepsLongLivedStreamInEveryActivePeakInterval(t *testing.T) {
	registry := NewRegistry()
	registry.StartHTTP("service")
	if first := registry.Collect()["service"]; first.HTTPActiveRequestsPeak != 1 {
		t.Fatalf("first active peak = %+v", first)
	}
	if second := registry.Collect()["service"]; second.HTTPActiveRequestsPeak != 1 {
		t.Fatalf("second active peak = %+v", second)
	}
	registry.FinishStreamingHTTP("service")
}

func TestRegistryForgetsServiceDuringConcurrentSampling(t *testing.T) {
	registry := NewRegistry()
	registry.StartHTTP("service")
	registry.AddIngress("service", 42)

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				_ = registry.Snapshot()
			}
		}()
	}
	registry.Forget("service")
	// An HTTP/TCP stream can finish after its route was withdrawn. Late
	// callbacks must not recreate counters for the deleted service.
	registry.FinishHTTP("service", 200, 0)
	registry.FinishTCP("service")
	registry.AddIngress("service", 1)
	registry.AddUDPEgress("service", 1)
	wait.Wait()
	if _, exists := registry.Snapshot()["service"]; exists {
		t.Fatal("forgotten service remained in registry snapshot")
	}
}
