package resourcemetrics

import (
	"math"
	"time"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

func stepForWindow(window time.Duration) (time.Duration, error) {
	switch window {
	case time.Hour:
		return time.Minute, nil
	case 6 * time.Hour:
		return 5 * time.Minute, nil
	case 24 * time.Hour:
		return 15 * time.Minute, nil
	case 7 * 24 * time.Hour:
		return time.Hour, nil
	case 30 * 24 * time.Hour:
		return 6 * time.Hour, nil
	default:
		return 0, ErrInvalidRange
	}
}

type historyBucket struct {
	observedAt  int64
	duration    int64
	running     bool
	memory      weightedHistoryValue
	memoryPeak  uint64
	cpu         weightedHistoryValue
	cpuPeak     int64
	ingress     weightedHistoryValue
	ingressPeak int64
	egress      weightedHistoryValue
	egressPeak  int64
	proxy       proxyHistoryBucket
}

type weightedHistoryValue struct {
	total    float64
	duration int64
}

type proxyHistoryBucket struct {
	present                                          bool
	httpRequests, http2xx, http3xx, http4xx, http5xx weightedHistoryValue
	tcpConnections, udpIngress, udpEgress            weightedHistoryValue
	httpRequestsPeak, tcpConnectionsPeak             float64
	udpIngressPeak, udpEgressPeak                    float64
	httpActive, tcpActive                            weightedHistoryValue
	httpActivePeak, tcpActivePeak                    int64
	httpTotal, tcpTotal                              uint64
	udpIngressTotal, udpEgressTotal                  uint64
	durationBuckets                                  [trafficmetrics.HTTPDurationBucketCount]uint64
}

func aggregateResources(samples []state.ResourceMetricSample, from int64, step time.Duration) []Point {
	buckets, order := newHistoryBuckets()
	for _, sample := range samples {
		bucket := historyMetricBucket(buckets, &order, from, step, sample.ObservedAt)
		bucket.add(sample.MetricValues, sample.Running)
	}
	return historyPoints(buckets, order)
}

func aggregateScopes(samples []state.AggregateMetricSample, from int64, step time.Duration) []Point {
	buckets, order := newHistoryBuckets()
	for _, sample := range samples {
		bucket := historyMetricBucket(buckets, &order, from, step, sample.ObservedAt)
		bucket.add(sample.MetricValues, sample.RunningResources > 0)
	}
	return historyPoints(buckets, order)
}

func newHistoryBuckets() (map[int64]*historyBucket, []int64) {
	return make(map[int64]*historyBucket), make([]int64, 0)
}

func historyMetricBucket(buckets map[int64]*historyBucket, order *[]int64, from int64, step time.Duration, observedAt int64) *historyBucket {
	key := (observedAt - from) / step.Milliseconds()
	bucket := buckets[key]
	if bucket == nil {
		bucket = &historyBucket{}
		buckets[key] = bucket
		*order = append(*order, key)
	}
	bucket.observedAt = observedAt
	return bucket
}

func (bucket *historyBucket) add(values state.MetricValues, running bool) {
	duration := max(values.DurationMillis, int64(1))
	bucket.duration += duration
	bucket.running = bucket.running || running
	bucket.memory.add(float64(values.MemoryBytes), duration)
	bucket.memoryPeak = max(bucket.memoryPeak, values.MemoryPeakBytes)
	if values.CPUMillicores != nil && values.CPUPeakMillicores != nil {
		bucket.cpu.add(float64(*values.CPUMillicores), values.CPUDurationMillis)
		bucket.cpuPeak = max(bucket.cpuPeak, *values.CPUPeakMillicores)
	}
	if values.NetworkIngressBytesPerSecond != nil && values.NetworkIngressPeakBytesPerSecond != nil {
		bucket.ingress.add(float64(*values.NetworkIngressBytesPerSecond), values.NetworkDurationMillis)
		bucket.ingressPeak = max(bucket.ingressPeak, *values.NetworkIngressPeakBytesPerSecond)
	}
	if values.NetworkEgressBytesPerSecond != nil && values.NetworkEgressPeakBytesPerSecond != nil {
		bucket.egress.add(float64(*values.NetworkEgressBytesPerSecond), values.NetworkDurationMillis)
		bucket.egressPeak = max(bucket.egressPeak, *values.NetworkEgressPeakBytesPerSecond)
	}
	bucket.proxy.add(values.Proxy, values.ProxyDurationMillis)
}

func historyPoints(buckets map[int64]*historyBucket, order []int64) []Point {
	points := make([]Point, 0, len(order))
	for _, key := range order {
		bucket := buckets[key]
		point := Point{
			ObservedAt: bucket.observedAt, DurationMillis: bucket.duration,
			MemoryBytes: uint64(math.Round(bucket.memory.average())), MemoryPeakBytes: bucket.memoryPeak,
			Running: bucket.running, Proxy: bucket.proxy.finish(),
		}
		if average, ok := bucket.cpu.intAverage(); ok {
			point.CPUMillicores = &average
			point.CPUPeakMillicores = int64Pointer(max(average, bucket.cpuPeak))
		}
		if average, ok := bucket.ingress.intAverage(); ok {
			point.NetworkIngressBytesPerSecond = &average
			point.NetworkIngressPeakBytesPerSecond = int64Pointer(max(average, bucket.ingressPeak))
		}
		if average, ok := bucket.egress.intAverage(); ok {
			point.NetworkEgressBytesPerSecond = &average
			point.NetworkEgressPeakBytesPerSecond = int64Pointer(max(average, bucket.egressPeak))
		}
		points = append(points, point)
	}
	return points
}

func (value *weightedHistoryValue) add(sample float64, duration int64) {
	value.total += sample * float64(duration)
	value.duration += duration
}

func (value weightedHistoryValue) average() float64 {
	if value.duration <= 0 {
		return 0
	}
	return value.total / float64(value.duration)
}

func (value weightedHistoryValue) floatAverage() *float64 {
	if value.duration <= 0 {
		return nil
	}
	return float64Pointer(value.average())
}

func (value weightedHistoryValue) intAverage() (int64, bool) {
	if value.duration <= 0 {
		return 0, false
	}
	return int64(math.Round(value.average())), true
}

func (bucket *proxyHistoryBucket) add(sample *state.ProxyMetricSample, duration int64) {
	if sample == nil {
		return
	}
	bucket.present = true
	bucket.httpRequests.addPointer(sample.HTTP.RequestsPerSecond, duration)
	bucket.http2xx.addPointer(sample.HTTP.Responses2xxPerSecond, duration)
	bucket.http3xx.addPointer(sample.HTTP.Responses3xxPerSecond, duration)
	bucket.http4xx.addPointer(sample.HTTP.Responses4xxPerSecond, duration)
	bucket.http5xx.addPointer(sample.HTTP.Responses5xxPerSecond, duration)
	bucket.tcpConnections.addPointer(sample.TCP.ConnectionsPerSecond, duration)
	bucket.udpIngress.addPointer(sample.UDP.IngressPacketsPerSecond, duration)
	bucket.udpEgress.addPointer(sample.UDP.EgressPacketsPerSecond, duration)
	bucket.httpActive.add(float64(sample.HTTP.ActiveRequests), duration)
	bucket.tcpActive.add(float64(sample.TCP.ActiveConnections), duration)
	if sample.HTTP.RequestsPeakPerSecond != nil {
		bucket.httpRequestsPeak = math.Max(bucket.httpRequestsPeak, *sample.HTTP.RequestsPeakPerSecond)
	}
	if sample.TCP.ConnectionsPeakPerSecond != nil {
		bucket.tcpConnectionsPeak = math.Max(bucket.tcpConnectionsPeak, *sample.TCP.ConnectionsPeakPerSecond)
	}
	if sample.UDP.IngressPacketsPeakPerSecond != nil {
		bucket.udpIngressPeak = math.Max(bucket.udpIngressPeak, *sample.UDP.IngressPacketsPeakPerSecond)
	}
	if sample.UDP.EgressPacketsPeakPerSecond != nil {
		bucket.udpEgressPeak = math.Max(bucket.udpEgressPeak, *sample.UDP.EgressPacketsPeakPerSecond)
	}
	bucket.httpActivePeak = max(bucket.httpActivePeak, sample.HTTP.ActiveRequestsPeak)
	bucket.tcpActivePeak = max(bucket.tcpActivePeak, sample.TCP.ActiveConnectionsPeak)
	bucket.httpTotal = sample.HTTP.RequestsTotal
	bucket.tcpTotal = sample.TCP.ConnectionsTotal
	bucket.udpIngressTotal = sample.UDP.IngressPacketsTotal
	bucket.udpEgressTotal = sample.UDP.EgressPacketsTotal
	for index, count := range sample.HTTP.DurationBuckets {
		bucket.durationBuckets[index] += count
	}
}

func (value *weightedHistoryValue) addPointer(sample *float64, duration int64) {
	if sample != nil {
		value.add(*sample, duration)
	}
}

func (bucket proxyHistoryBucket) finish() *ProxyMetrics {
	if !bucket.present {
		return nil
	}
	metrics := &ProxyMetrics{
		HTTP: HTTPMetrics{
			RequestsPerSecond: bucket.httpRequests.floatAverage(), RequestsTotal: bucket.httpTotal,
			ActiveRequests: int64(math.Round(bucket.httpActive.average())), ActiveRequestsPeak: bucket.httpActivePeak,
			Responses2xxPerSecond: bucket.http2xx.floatAverage(), Responses3xxPerSecond: bucket.http3xx.floatAverage(),
			Responses4xxPerSecond: bucket.http4xx.floatAverage(), Responses5xxPerSecond: bucket.http5xx.floatAverage(),
		},
		TCP: TCPMetrics{
			ConnectionsPerSecond: bucket.tcpConnections.floatAverage(), ConnectionsTotal: bucket.tcpTotal,
			ActiveConnections: int64(math.Round(bucket.tcpActive.average())), ActiveConnectionsPeak: bucket.tcpActivePeak,
		},
		UDP: UDPMetrics{
			IngressPacketsPerSecond: bucket.udpIngress.floatAverage(), EgressPacketsPerSecond: bucket.udpEgress.floatAverage(),
			IngressPacketsTotal: bucket.udpIngressTotal, EgressPacketsTotal: bucket.udpEgressTotal,
		},
	}
	if metrics.HTTP.RequestsPerSecond != nil {
		metrics.HTTP.RequestsPeakPerSecond = float64Pointer(math.Max(*metrics.HTTP.RequestsPerSecond, bucket.httpRequestsPeak))
	}
	if metrics.TCP.ConnectionsPerSecond != nil {
		metrics.TCP.ConnectionsPeakPerSecond = float64Pointer(math.Max(*metrics.TCP.ConnectionsPerSecond, bucket.tcpConnectionsPeak))
	}
	if metrics.UDP.IngressPacketsPerSecond != nil {
		metrics.UDP.IngressPacketsPeakPerSecond = float64Pointer(math.Max(*metrics.UDP.IngressPacketsPerSecond, bucket.udpIngressPeak))
	}
	if metrics.UDP.EgressPacketsPerSecond != nil {
		metrics.UDP.EgressPacketsPeakPerSecond = float64Pointer(math.Max(*metrics.UDP.EgressPacketsPerSecond, bucket.udpEgressPeak))
	}
	setLatencyPercentiles(&metrics.HTTP, bucket.durationBuckets)
	return metrics
}
