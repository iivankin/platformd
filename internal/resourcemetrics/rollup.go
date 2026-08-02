package resourcemetrics

import (
	"math"
	"time"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type minuteAccumulator struct {
	startedAt    time.Time
	lastAt       time.Time
	resources    map[metricKey]*metricRollup
	projects     map[string]*metricRollup
	installation *metricRollup
	host         *metricRollup
}

type metricRollup struct {
	duration int64
	running  bool

	memoryWeighted float64
	memoryDuration int64
	memoryPeak     uint64

	cpuUsageMicros uint64
	cpuWeighted    float64
	cpuDuration    int64
	cpuPeak        int64
	cpuSeen        bool

	ingressBytes    uint64
	ingressWeighted float64
	ingressDuration int64
	ingressPeak     int64
	ingressSeen     bool
	egressBytes     uint64
	egressWeighted  float64
	egressDuration  int64
	egressPeak      int64
	egressSeen      bool

	runningResources int
	totalResources   int
	proxy            *proxyRollup
}

type proxyRollup struct {
	present  bool
	duration int64

	httpRequests, http2xx, http3xx, http4xx, http5xx uint64
	tcpConnections                                   uint64
	udpIngress, udpEgress                            uint64
	httpDurationBuckets                              [trafficmetrics.HTTPDurationBucketCount]uint64

	httpRequestsPeak, tcpConnectionsPeak  float64
	udpIngressPeak, udpEgressPeak         float64
	httpActiveWeighted, tcpActiveWeighted float64
	httpActivePeak, tcpActivePeak         int64
	httpTotal, tcpTotal                   uint64
	udpIngressTotal, udpEgressTotal       uint64
}

func newMinuteAccumulator(
	now time.Time,
	resources map[metricKey]Current,
	projects map[string]Current,
	installation Current,
) *minuteAccumulator {
	accumulator := &minuteAccumulator{
		startedAt: now, lastAt: now,
		resources:    make(map[metricKey]*metricRollup, len(resources)),
		projects:     make(map[string]*metricRollup, len(projects)),
		installation: &metricRollup{},
	}
	for key, current := range resources {
		rollup := &metricRollup{}
		rollup.seedCurrent(current)
		accumulator.resources[key] = rollup
	}
	for projectID, current := range projects {
		rollup := &metricRollup{}
		rollup.seedCurrent(current)
		accumulator.projects[projectID] = rollup
	}
	accumulator.installation.seedCurrent(installation)
	if installation.Host != nil {
		accumulator.host = &metricRollup{}
		accumulator.host.seedHost(*installation.Host)
	}
	return accumulator
}

func (accumulator *minuteAccumulator) add(
	now time.Time,
	activeResources map[metricKey]struct{},
	resources map[metricKey]Current,
	projects map[string]Current,
	installation Current,
) {
	fallback := now.Sub(accumulator.lastAt)
	if fallback <= 0 {
		return
	}
	for key := range accumulator.resources {
		if _, exists := activeResources[key]; !exists {
			delete(accumulator.resources, key)
		}
	}
	for key, current := range resources {
		rollup := accumulator.resources[key]
		if rollup == nil {
			rollup = &metricRollup{}
			rollup.seedCurrent(current)
			accumulator.resources[key] = rollup
			continue
		}
		rollup.addCurrent(current, fallback)
	}
	for projectID := range accumulator.projects {
		if _, exists := projects[projectID]; !exists {
			delete(accumulator.projects, projectID)
		}
	}
	for projectID, current := range projects {
		rollup := accumulator.projects[projectID]
		if rollup == nil {
			rollup = &metricRollup{}
			rollup.seedCurrent(current)
			accumulator.projects[projectID] = rollup
			continue
		}
		rollup.addCurrent(current, fallback)
	}
	accumulator.installation.addCurrent(installation, fallback)
	if installation.Host != nil {
		if accumulator.host == nil {
			accumulator.host = &metricRollup{}
			accumulator.host.seedHost(*installation.Host)
		} else {
			accumulator.host.addHost(*installation.Host, fallback)
		}
	}
	accumulator.lastAt = now
}

func (accumulator *minuteAccumulator) ready(now time.Time, interval time.Duration) bool {
	return now.Sub(accumulator.startedAt) >= interval
}

func (accumulator *minuteAccumulator) batch(now time.Time, retention time.Duration) state.MetricBatch {
	batch := state.MetricBatch{
		Resources:       make([]state.ResourceMetricSample, 0, len(accumulator.resources)),
		Aggregates:      make([]state.AggregateMetricSample, 0, len(accumulator.projects)+2),
		RetentionCutoff: now.Add(-retention).UnixMilli(),
	}
	for key, rollup := range accumulator.resources {
		if values, ok := rollup.values(); ok {
			batch.Resources = append(batch.Resources, state.ResourceMetricSample{
				Kind: key.kind, ResourceID: key.id, ObservedAt: now.UnixMilli(), MetricValues: values,
			})
		}
	}
	for projectID, rollup := range accumulator.projects {
		if values, ok := rollup.values(); ok {
			batch.Aggregates = append(batch.Aggregates, state.AggregateMetricSample{
				ScopeKind: "project", ScopeID: projectID, ObservedAt: now.UnixMilli(),
				RunningResources: rollup.runningResources, TotalResources: rollup.totalResources,
				MetricValues: values,
			})
		}
	}
	if values, ok := accumulator.installation.values(); ok {
		batch.Aggregates = append(batch.Aggregates, state.AggregateMetricSample{
			ScopeKind: "installation", ScopeID: "installation", ObservedAt: now.UnixMilli(),
			RunningResources: accumulator.installation.runningResources,
			TotalResources:   accumulator.installation.totalResources,
			MetricValues:     values,
		})
	}
	if accumulator.host != nil {
		if values, ok := accumulator.host.values(); ok {
			batch.Aggregates = append(batch.Aggregates, state.AggregateMetricSample{
				ScopeKind: "host", ScopeID: "host", ObservedAt: now.UnixMilli(),
				RunningResources: 1, TotalResources: 1, MetricValues: values,
			})
		}
	}
	return batch
}

func (rollup *metricRollup) seedCurrent(current Current) {
	rollup.running = current.Running
	rollup.memoryPeak = max(current.MemoryPeakBytes, current.MemoryBytes)
	rollup.runningResources = current.RunningResources
	rollup.totalResources = current.TotalResources
	rollup.seedProxy(current.Proxy)
}

func (rollup *metricRollup) seedHost(current HostCurrent) {
	rollup.running = true
	rollup.memoryPeak = max(current.MemoryPeakBytes, current.MemoryUsedBytes)
}

func (rollup *metricRollup) addCurrent(current Current, fallback time.Duration) {
	duration := intervalMillis(current.interval.duration, fallback)
	rollup.duration += duration
	rollup.running = current.Running
	rollup.runningResources = current.RunningResources
	rollup.totalResources = current.TotalResources
	rollup.addMemory(current.MemoryBytes, current.MemoryPeakBytes, duration)
	rollup.addCPU(current.CPUMillicores, current.CPUPeakMillicores, current.interval.cpuUsageMicros, duration)
	rollup.addNetwork(current, duration)
	rollup.addProxy(current.Proxy, current.interval.traffic, duration)
}

func (rollup *metricRollup) addHost(current HostCurrent, fallback time.Duration) {
	duration := intervalMillis(current.interval.duration, fallback)
	rollup.duration += duration
	rollup.running = true
	rollup.addMemory(current.MemoryUsedBytes, current.MemoryPeakBytes, duration)
	rollup.addWeightedCPU(current.CPUMillicores, current.CPUPeakMillicores, duration)
	rollup.addWeightedNetwork(
		current.NetworkIngressBytesPerSecond, current.NetworkIngressPeakBytesPerSecond,
		current.NetworkEgressBytesPerSecond, current.NetworkEgressPeakBytesPerSecond,
		duration,
	)
}

func (rollup *metricRollup) addMemory(value, peak uint64, duration int64) {
	rollup.memoryWeighted += float64(value) * float64(duration)
	rollup.memoryDuration += duration
	rollup.memoryPeak = max(rollup.memoryPeak, value, peak)
}

func (rollup *metricRollup) addCPU(value, peak *int64, exactDelta *uint64, duration int64) {
	if value == nil || peak == nil {
		return
	}
	rollup.cpuSeen = true
	rollup.cpuPeak = max(rollup.cpuPeak, *peak)
	rollup.cpuDuration += duration
	if exactDelta != nil {
		rollup.cpuUsageMicros += *exactDelta
		return
	}
	rollup.cpuWeighted += float64(*value) * float64(duration)
}

func (rollup *metricRollup) addWeightedCPU(value, peak *int64, duration int64) {
	if value == nil || peak == nil {
		return
	}
	rollup.cpuSeen = true
	rollup.cpuPeak = max(rollup.cpuPeak, *peak)
	rollup.cpuDuration += duration
	rollup.cpuWeighted += float64(*value) * float64(duration)
}

func (rollup *metricRollup) addNetwork(current Current, duration int64) {
	if current.NetworkIngressBytesPerSecond == nil || current.NetworkIngressPeakBytesPerSecond == nil ||
		current.NetworkEgressBytesPerSecond == nil || current.NetworkEgressPeakBytesPerSecond == nil {
		return
	}
	rollup.ingressSeen, rollup.egressSeen = true, true
	rollup.ingressPeak = max(rollup.ingressPeak, *current.NetworkIngressPeakBytesPerSecond)
	rollup.egressPeak = max(rollup.egressPeak, *current.NetworkEgressPeakBytesPerSecond)
	rollup.ingressDuration += duration
	rollup.egressDuration += duration
	if current.interval.networkIngressBytes != nil && current.interval.networkEgressBytes != nil {
		rollup.ingressBytes += *current.interval.networkIngressBytes
		rollup.egressBytes += *current.interval.networkEgressBytes
		return
	}
	rollup.ingressWeighted += float64(*current.NetworkIngressBytesPerSecond) * float64(duration)
	rollup.egressWeighted += float64(*current.NetworkEgressBytesPerSecond) * float64(duration)
}

func (rollup *metricRollup) addWeightedNetwork(ingress, ingressPeak, egress, egressPeak *int64, duration int64) {
	if ingress == nil || ingressPeak == nil || egress == nil || egressPeak == nil {
		return
	}
	rollup.ingressSeen, rollup.egressSeen = true, true
	rollup.ingressPeak = max(rollup.ingressPeak, *ingressPeak)
	rollup.egressPeak = max(rollup.egressPeak, *egressPeak)
	rollup.ingressDuration += duration
	rollup.egressDuration += duration
	rollup.ingressWeighted += float64(*ingress) * float64(duration)
	rollup.egressWeighted += float64(*egress) * float64(duration)
}

func (rollup *metricRollup) seedProxy(metrics *ProxyMetrics) {
	if metrics == nil {
		return
	}
	rollup.proxy = &proxyRollup{present: true}
	rollup.proxy.capture(metrics)
}

func (rollup *metricRollup) addProxy(metrics *ProxyMetrics, delta *trafficDelta, duration int64) {
	if metrics == nil {
		return
	}
	if rollup.proxy == nil {
		rollup.proxy = &proxyRollup{present: true}
	}
	proxy := rollup.proxy
	proxy.capture(metrics)
	if metrics.HTTP.RequestsPerSecond == nil || metrics.TCP.ConnectionsPerSecond == nil ||
		metrics.UDP.IngressPacketsPerSecond == nil || metrics.UDP.EgressPacketsPerSecond == nil {
		return
	}
	proxy.duration += duration
	proxy.httpRequestsPeak = math.Max(proxy.httpRequestsPeak, *metrics.HTTP.RequestsPeakPerSecond)
	proxy.tcpConnectionsPeak = math.Max(proxy.tcpConnectionsPeak, *metrics.TCP.ConnectionsPeakPerSecond)
	proxy.udpIngressPeak = math.Max(proxy.udpIngressPeak, *metrics.UDP.IngressPacketsPeakPerSecond)
	proxy.udpEgressPeak = math.Max(proxy.udpEgressPeak, *metrics.UDP.EgressPacketsPeakPerSecond)
	proxy.httpActiveWeighted += float64(metrics.HTTP.ActiveRequests) * float64(duration)
	proxy.tcpActiveWeighted += float64(metrics.TCP.ActiveConnections) * float64(duration)
	proxy.httpActivePeak = max(proxy.httpActivePeak, metrics.HTTP.ActiveRequestsPeak)
	proxy.tcpActivePeak = max(proxy.tcpActivePeak, metrics.TCP.ActiveConnectionsPeak)
	if delta == nil {
		proxy.httpRequests += roundedDelta(*metrics.HTTP.RequestsPerSecond, duration)
		proxy.http2xx += roundedDelta(pointerValue(metrics.HTTP.Responses2xxPerSecond), duration)
		proxy.http3xx += roundedDelta(pointerValue(metrics.HTTP.Responses3xxPerSecond), duration)
		proxy.http4xx += roundedDelta(pointerValue(metrics.HTTP.Responses4xxPerSecond), duration)
		proxy.http5xx += roundedDelta(pointerValue(metrics.HTTP.Responses5xxPerSecond), duration)
		proxy.tcpConnections += roundedDelta(*metrics.TCP.ConnectionsPerSecond, duration)
		proxy.udpIngress += roundedDelta(*metrics.UDP.IngressPacketsPerSecond, duration)
		proxy.udpEgress += roundedDelta(*metrics.UDP.EgressPacketsPerSecond, duration)
		return
	}
	proxy.httpRequests += delta.httpRequests
	proxy.http2xx += delta.http2xx
	proxy.http3xx += delta.http3xx
	proxy.http4xx += delta.http4xx
	proxy.http5xx += delta.http5xx
	proxy.tcpConnections += delta.tcpConnections
	proxy.udpIngress += delta.udpIngress
	proxy.udpEgress += delta.udpEgress
	for index, count := range delta.httpDurationBuckets {
		proxy.httpDurationBuckets[index] += count
	}
}

func (proxy *proxyRollup) capture(metrics *ProxyMetrics) {
	proxy.present = true
	proxy.httpTotal = metrics.HTTP.RequestsTotal
	proxy.tcpTotal = metrics.TCP.ConnectionsTotal
	proxy.udpIngressTotal = metrics.UDP.IngressPacketsTotal
	proxy.udpEgressTotal = metrics.UDP.EgressPacketsTotal
	proxy.httpActivePeak = max(proxy.httpActivePeak, metrics.HTTP.ActiveRequestsPeak)
	proxy.tcpActivePeak = max(proxy.tcpActivePeak, metrics.TCP.ActiveConnectionsPeak)
}

func (rollup *metricRollup) values() (state.MetricValues, bool) {
	if rollup.duration <= 0 {
		return state.MetricValues{}, false
	}
	values := state.MetricValues{
		DurationMillis:  rollup.duration,
		MemoryBytes:     weightedUint64(rollup.memoryWeighted, rollup.memoryDuration),
		MemoryPeakBytes: rollup.memoryPeak,
		Running:         rollup.running,
	}
	if rollup.cpuSeen && rollup.cpuDuration > 0 {
		average := weightedCPU(rollup.cpuUsageMicros, rollup.cpuWeighted, rollup.cpuDuration)
		values.CPUDurationMillis = rollup.cpuDuration
		values.CPUMillicores = &average
		values.CPUPeakMillicores = int64Pointer(max(average, rollup.cpuPeak))
	}
	if rollup.ingressSeen && rollup.egressSeen && rollup.ingressDuration > 0 && rollup.egressDuration > 0 {
		ingress := weightedRate(rollup.ingressBytes, rollup.ingressWeighted, rollup.ingressDuration)
		egress := weightedRate(rollup.egressBytes, rollup.egressWeighted, rollup.egressDuration)
		values.NetworkDurationMillis = min(rollup.ingressDuration, rollup.egressDuration)
		values.NetworkIngressBytesPerSecond = &ingress
		values.NetworkIngressPeakBytesPerSecond = int64Pointer(max(ingress, rollup.ingressPeak))
		values.NetworkEgressBytesPerSecond = &egress
		values.NetworkEgressPeakBytesPerSecond = int64Pointer(max(egress, rollup.egressPeak))
	}
	values.Proxy = rollup.proxy.values()
	if values.Proxy != nil && rollup.proxy.duration > 0 {
		values.ProxyDurationMillis = rollup.proxy.duration
	}
	return values, true
}

func (proxy *proxyRollup) values() *state.ProxyMetricSample {
	if proxy == nil || !proxy.present {
		return nil
	}
	sample := &state.ProxyMetricSample{}
	sample.HTTP.RequestsTotal = proxy.httpTotal
	sample.HTTP.ActiveRequestsPeak = proxy.httpActivePeak
	sample.TCP.ConnectionsTotal = proxy.tcpTotal
	sample.TCP.ActiveConnectionsPeak = proxy.tcpActivePeak
	sample.UDP.IngressPacketsTotal = proxy.udpIngressTotal
	sample.UDP.EgressPacketsTotal = proxy.udpEgressTotal
	sample.HTTP.DurationBuckets = proxy.httpDurationBuckets
	if proxy.duration <= 0 {
		return sample
	}
	sample.HTTP.RequestsPerSecond = float64Pointer(counterRate(proxy.httpRequests, proxy.duration))
	sample.HTTP.RequestsPeakPerSecond = float64Pointer(math.Max(*sample.HTTP.RequestsPerSecond, proxy.httpRequestsPeak))
	sample.HTTP.Responses2xxPerSecond = float64Pointer(counterRate(proxy.http2xx, proxy.duration))
	sample.HTTP.Responses3xxPerSecond = float64Pointer(counterRate(proxy.http3xx, proxy.duration))
	sample.HTTP.Responses4xxPerSecond = float64Pointer(counterRate(proxy.http4xx, proxy.duration))
	sample.HTTP.Responses5xxPerSecond = float64Pointer(counterRate(proxy.http5xx, proxy.duration))
	sample.TCP.ConnectionsPerSecond = float64Pointer(counterRate(proxy.tcpConnections, proxy.duration))
	sample.TCP.ConnectionsPeakPerSecond = float64Pointer(math.Max(*sample.TCP.ConnectionsPerSecond, proxy.tcpConnectionsPeak))
	sample.UDP.IngressPacketsPerSecond = float64Pointer(counterRate(proxy.udpIngress, proxy.duration))
	sample.UDP.IngressPacketsPeakPerSecond = float64Pointer(math.Max(*sample.UDP.IngressPacketsPerSecond, proxy.udpIngressPeak))
	sample.UDP.EgressPacketsPerSecond = float64Pointer(counterRate(proxy.udpEgress, proxy.duration))
	sample.UDP.EgressPacketsPeakPerSecond = float64Pointer(math.Max(*sample.UDP.EgressPacketsPerSecond, proxy.udpEgressPeak))
	sample.HTTP.ActiveRequests = int64(math.Round(proxy.httpActiveWeighted / float64(proxy.duration)))
	sample.TCP.ActiveConnections = int64(math.Round(proxy.tcpActiveWeighted / float64(proxy.duration)))
	return sample
}

func intervalMillis(primary time.Duration, fallback time.Duration) int64 {
	if primary > 0 {
		return max(primary.Milliseconds(), int64(1))
	}
	return max(fallback.Milliseconds(), int64(1))
}

func weightedUint64(total float64, duration int64) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(math.Round(total / float64(duration)))
}

func weightedRate(exactDelta uint64, weighted float64, duration int64) int64 {
	return int64(math.Round((float64(exactDelta)*1000 + weighted) / float64(duration)))
}

func weightedCPU(usageMicros uint64, weighted float64, duration int64) int64 {
	return int64(math.Round((float64(usageMicros) + weighted) / float64(duration)))
}

func counterRate(delta uint64, duration int64) float64 {
	return float64(delta) * 1000 / float64(duration)
}

func roundedDelta(rate float64, duration int64) uint64 {
	return uint64(math.Round(rate * float64(duration) / 1000))
}

func pointerValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
