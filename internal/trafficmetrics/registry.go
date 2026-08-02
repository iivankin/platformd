package trafficmetrics

import (
	"context"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const HTTPDurationBucketCount = 13

var HTTPDurationUpperBoundsMillis = [HTTPDurationBucketCount - 1]uint64{
	5, 10, 25, 50, 100, 250, 500, 1_000, 2_500, 5_000, 10_000, 30_000,
}

type Counters struct {
	IngressBytes                   uint64
	EgressBytes                    uint64
	HTTPRequestsTotal              uint64
	HTTPRequestsPeakPerSecond      float64
	HTTPActiveRequests             int64
	HTTPActiveRequestsPeak         int64
	HTTPResponses2xxTotal          uint64
	HTTPResponses3xxTotal          uint64
	HTTPResponses4xxTotal          uint64
	HTTPResponses5xxTotal          uint64
	HTTPDurationBuckets            [HTTPDurationBucketCount]uint64
	TCPConnectionsTotal            uint64
	TCPConnectionsPeakPerSecond    float64
	TCPActiveConnections           int64
	TCPActiveConnectionsPeak       int64
	UDPIngressPackets              uint64
	UDPIngressPacketsPeakPerSecond float64
	UDPEgressPackets               uint64
	UDPEgressPacketsPeakPerSecond  float64
	ProtocolRatePeaksAvailable     bool
}

type serviceCounters struct {
	ingress          atomic.Uint64
	egress           atomic.Uint64
	httpRequests     atomic.Uint64
	httpActive       atomic.Int64
	httpActivePeak   atomic.Int64
	httpResponses2xx atomic.Uint64
	httpResponses3xx atomic.Uint64
	httpResponses4xx atomic.Uint64
	httpResponses5xx atomic.Uint64
	httpDuration     [HTTPDurationBucketCount]atomic.Uint64
	tcpConnections   atomic.Uint64
	tcpActive        atomic.Int64
	tcpActivePeak    atomic.Int64
	udpIngress       atomic.Uint64
	udpEgress        atomic.Uint64
	rateMu           sync.Mutex
	ratePrevious     protocolTotals
	ratePreviousAt   time.Time
	ratePeaks        protocolRates
	ratePeakSamples  uint64
}

type protocolTotals struct {
	http, tcp, udpIngress, udpEgress uint64
}

type protocolRates struct {
	http, tcp, udpIngress, udpEgress float64
}

// Registry holds cumulative public payload counters. Services are stored in a
// sync.Map because routes are read-heavy and are only added or forgotten at
// lifecycle boundaries; individual counters remain lock-free atomics.
type Registry struct {
	services sync.Map
	retired  sync.Map
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (registry *Registry) AddIngress(serviceID string, bytes uint64) {
	if registry == nil || serviceID == "" || bytes == 0 {
		return
	}
	counters := registry.counters(serviceID)
	if counters != nil {
		counters.ingress.Add(bytes)
	}
}

func (registry *Registry) AddEgress(serviceID string, bytes uint64) {
	if registry == nil || serviceID == "" || bytes == 0 {
		return
	}
	counters := registry.counters(serviceID)
	if counters != nil {
		counters.egress.Add(bytes)
	}
}

func (registry *Registry) StartHTTP(serviceID string) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters == nil {
		return
	}
	counters.httpRequests.Add(1)
	active := counters.httpActive.Add(1)
	atomicMaxInt64(&counters.httpActivePeak, active)
}

func (registry *Registry) FinishHTTP(serviceID string, statusCode int, duration time.Duration) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters == nil {
		return
	}
	counters.httpActive.Add(-1)
	observeHTTPResponse(counters, statusCode, duration)
}

// ObserveStreamingHTTP records header latency and status as soon as a
// long-lived response is established. FinishStreamingHTTP keeps the request
// active until its SSE or upgraded connection actually closes.
func (registry *Registry) ObserveStreamingHTTP(serviceID string, statusCode int, duration time.Duration) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters != nil {
		observeHTTPResponse(counters, statusCode, duration)
	}
}

func (registry *Registry) FinishStreamingHTTP(serviceID string) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters != nil {
		counters.httpActive.Add(-1)
	}
}

func observeHTTPResponse(counters *serviceCounters, statusCode int, duration time.Duration) {
	switch statusCode / 100 {
	case 2:
		counters.httpResponses2xx.Add(1)
	case 3:
		counters.httpResponses3xx.Add(1)
	case 4:
		counters.httpResponses4xx.Add(1)
	case 5:
		counters.httpResponses5xx.Add(1)
	}
	milliseconds := uint64(max(duration.Milliseconds(), 0))
	bucket := len(HTTPDurationUpperBoundsMillis)
	for index, upperBound := range HTTPDurationUpperBoundsMillis {
		if milliseconds <= upperBound {
			bucket = index
			break
		}
	}
	counters.httpDuration[bucket].Add(1)
}

func (registry *Registry) StartTCP(serviceID string) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters == nil {
		return
	}
	counters.tcpConnections.Add(1)
	active := counters.tcpActive.Add(1)
	atomicMaxInt64(&counters.tcpActivePeak, active)
}

func (registry *Registry) FinishTCP(serviceID string) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters != nil {
		counters.tcpActive.Add(-1)
	}
}

func (registry *Registry) AddUDPIngress(serviceID string, bytes uint64) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters == nil {
		return
	}
	if bytes > 0 {
		counters.ingress.Add(bytes)
	}
	counters.udpIngress.Add(1)
}

func (registry *Registry) AddUDPEgress(serviceID string, bytes uint64) {
	if registry == nil || serviceID == "" {
		return
	}
	counters := registry.counters(serviceID)
	if counters == nil {
		return
	}
	if bytes > 0 {
		counters.egress.Add(bytes)
	}
	counters.udpEgress.Add(1)
}

func (registry *Registry) Snapshot() map[string]Counters {
	return registry.snapshot(false)
}

// Collect returns interval peaks to the metrics collector and starts a fresh
// peak interval without resetting the cumulative counters.
func (registry *Registry) Collect() map[string]Counters {
	return registry.snapshot(true)
}

func (registry *Registry) snapshot(resetPeaks bool) map[string]Counters {
	result := make(map[string]Counters)
	if registry == nil {
		return result
	}
	registry.services.Range(func(key, value any) bool {
		serviceID := key.(string)
		if _, retired := registry.retired.Load(serviceID); retired {
			return true
		}
		counters := value.(*serviceCounters)
		var httpActive, tcpActive, httpActivePeak, tcpActivePeak int64
		if resetPeaks {
			httpActivePeak = counters.httpActivePeak.Swap(0)
			tcpActivePeak = counters.tcpActivePeak.Swap(0)
			httpActive = max(counters.httpActive.Load(), 0)
			tcpActive = max(counters.tcpActive.Load(), 0)
			// A stream that crosses the collection boundary belongs to both
			// intervals, so seed the fresh peak with the coincident active count.
			atomicMaxInt64(&counters.httpActivePeak, httpActive)
			atomicMaxInt64(&counters.tcpActivePeak, tcpActive)
			httpActivePeak = max(httpActivePeak, httpActive)
			tcpActivePeak = max(tcpActivePeak, tcpActive)
		} else {
			httpActive = max(counters.httpActive.Load(), 0)
			tcpActive = max(counters.tcpActive.Load(), 0)
			httpActivePeak = max(counters.httpActivePeak.Load(), httpActive)
			tcpActivePeak = max(counters.tcpActivePeak.Load(), tcpActive)
		}
		rates, rateSamples := counters.protocolRatePeaks(resetPeaks)
		snapshot := Counters{
			IngressBytes:      counters.ingress.Load(),
			EgressBytes:       counters.egress.Load(),
			HTTPRequestsTotal: counters.httpRequests.Load(), HTTPRequestsPeakPerSecond: rates.http,
			HTTPActiveRequests: httpActive, HTTPActiveRequestsPeak: httpActivePeak,
			HTTPResponses2xxTotal: counters.httpResponses2xx.Load(), HTTPResponses3xxTotal: counters.httpResponses3xx.Load(),
			HTTPResponses4xxTotal: counters.httpResponses4xx.Load(), HTTPResponses5xxTotal: counters.httpResponses5xx.Load(),
			TCPConnectionsTotal: counters.tcpConnections.Load(), TCPConnectionsPeakPerSecond: rates.tcp,
			TCPActiveConnections: tcpActive, TCPActiveConnectionsPeak: tcpActivePeak,
			UDPIngressPackets: counters.udpIngress.Load(), UDPIngressPacketsPeakPerSecond: rates.udpIngress,
			UDPEgressPackets: counters.udpEgress.Load(), UDPEgressPacketsPeakPerSecond: rates.udpEgress,
			ProtocolRatePeaksAvailable: rateSamples > 0,
		}
		for index := range snapshot.HTTPDurationBuckets {
			snapshot.HTTPDurationBuckets[index] = counters.httpDuration[index].Load()
		}
		result[serviceID] = snapshot
		return true
	})
	return result
}

// RunProtocolRateSampler captures aligned one-second protocol rate windows.
// It reads only process-local atomics and does not accelerate cgroup or
// nftables collection.
func (registry *Registry) RunProtocolRateSampler(ctx context.Context, interval time.Duration) error {
	if registry == nil || interval <= 0 {
		return nil
	}
	registry.sampleProtocolRates(time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sampledAt := <-ticker.C:
			registry.sampleProtocolRates(sampledAt)
		}
	}
}

func (registry *Registry) sampleProtocolRates(sampledAt time.Time) {
	if registry == nil {
		return
	}
	registry.services.Range(func(key, value any) bool {
		serviceID := key.(string)
		if _, retired := registry.retired.Load(serviceID); retired {
			return true
		}
		value.(*serviceCounters).sampleProtocolRates(sampledAt)
		return true
	})
}

func (counters *serviceCounters) sampleProtocolRates(sampledAt time.Time) {
	current := counters.protocolTotals()
	counters.rateMu.Lock()
	defer counters.rateMu.Unlock()
	if counters.ratePreviousAt.IsZero() {
		counters.ratePrevious = current
		counters.ratePreviousAt = sampledAt
		return
	}
	elapsed := sampledAt.Sub(counters.ratePreviousAt)
	previous := counters.ratePrevious
	counters.ratePrevious = current
	counters.ratePreviousAt = sampledAt
	if elapsed <= 0 || !current.atLeast(previous) {
		return
	}
	counters.ratePeaks.http = math.Max(counters.ratePeaks.http, protocolRate(current.http-previous.http, elapsed))
	counters.ratePeaks.tcp = math.Max(counters.ratePeaks.tcp, protocolRate(current.tcp-previous.tcp, elapsed))
	counters.ratePeaks.udpIngress = math.Max(counters.ratePeaks.udpIngress, protocolRate(current.udpIngress-previous.udpIngress, elapsed))
	counters.ratePeaks.udpEgress = math.Max(counters.ratePeaks.udpEgress, protocolRate(current.udpEgress-previous.udpEgress, elapsed))
	counters.ratePeakSamples++
}

func (counters *serviceCounters) protocolTotals() protocolTotals {
	return protocolTotals{
		http: counters.httpRequests.Load(), tcp: counters.tcpConnections.Load(),
		udpIngress: counters.udpIngress.Load(), udpEgress: counters.udpEgress.Load(),
	}
}

func (current protocolTotals) atLeast(previous protocolTotals) bool {
	return current.http >= previous.http && current.tcp >= previous.tcp &&
		current.udpIngress >= previous.udpIngress && current.udpEgress >= previous.udpEgress
}

func (counters *serviceCounters) protocolRatePeaks(reset bool) (protocolRates, uint64) {
	counters.rateMu.Lock()
	defer counters.rateMu.Unlock()
	rates, samples := counters.ratePeaks, counters.ratePeakSamples
	if reset {
		counters.ratePeaks = protocolRates{}
		counters.ratePeakSamples = 0
	}
	return rates, samples
}

func protocolRate(delta uint64, elapsed time.Duration) float64 {
	return float64(delta) * float64(time.Second) / float64(elapsed)
}

func atomicMaxInt64(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current; current = target.Load() {
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

func (registry *Registry) counters(serviceID string) *serviceCounters {
	if current, exists := registry.services.Load(serviceID); exists {
		return current.(*serviceCounters)
	}
	if _, retired := registry.retired.Load(serviceID); retired {
		return nil
	}
	created := &serviceCounters{}
	current, _ := registry.services.LoadOrStore(serviceID, created)
	// Forget races with in-flight proxy callbacks. Recheck after insertion so a
	// callback that lost that race cannot recreate a deleted service entry.
	if _, retired := registry.retired.Load(serviceID); retired {
		registry.services.CompareAndDelete(serviceID, current)
		return nil
	}
	return current.(*serviceCounters)
}

// Forget removes counters after the service's public routes and runtime have
// been withdrawn. Service IDs are immutable and are never reused.
func (registry *Registry) Forget(serviceID string) {
	if registry == nil || serviceID == "" {
		return
	}
	// IDs are immutable and never reused, so the small retirement marker can
	// safely reject late callbacks from requests that outlived route removal.
	registry.retired.Store(serviceID, struct{}{})
	registry.services.Delete(serviceID)
}

type countingReader struct {
	reader io.Reader
	add    func(uint64)
}

func (reader countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.add(uint64(count))
	}
	return count, err
}

func CountReader(reader io.Reader, add func(uint64)) io.Reader {
	if reader == nil || add == nil {
		return reader
	}
	return countingReader{reader: reader, add: add}
}
