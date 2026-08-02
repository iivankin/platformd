package resourcemetrics

import (
	"math"
	"time"

	"github.com/iivankin/platformd/internal/hostmetrics"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

func calculateRates(current *Current, previous previousSample, counters trafficmetrics.Counters, sampledAt time.Time) {
	elapsed := sampledAt.Sub(previous.at)
	if elapsed <= 0 {
		return
	}
	current.interval.duration = elapsed
	calculateProxyRates(current, previous.traffic, counters, elapsed)
	if previous.networkAvailable && current.NetworkAvailable &&
		current.NetworkRXBytes >= previous.networkIngress && current.NetworkTXBytes >= previous.networkEgress {
		ingressDelta := current.NetworkRXBytes - previous.networkIngress
		egressDelta := current.NetworkTXBytes - previous.networkEgress
		ingress := ratePerSecond(ingressDelta, elapsed)
		egress := ratePerSecond(egressDelta, elapsed)
		current.NetworkIngressBytesPerSecond = &ingress
		current.NetworkIngressPeakBytesPerSecond = int64Pointer(ingress)
		current.NetworkEgressBytesPerSecond = &egress
		current.NetworkEgressPeakBytesPerSecond = int64Pointer(egress)
		current.interval.networkIngressBytes = uint64Pointer(ingressDelta)
		current.interval.networkEgressBytes = uint64Pointer(egressDelta)
	}
	if previous.running && current.Running && current.CPUUsageMicros >= previous.cpuUsageMicros {
		delta := current.CPUUsageMicros - previous.cpuUsageMicros
		cpu := int64(math.Round(float64(delta) * float64(time.Millisecond) / float64(elapsed)))
		current.CPUMillicores = &cpu
		current.CPUPeakMillicores = int64Pointer(cpu)
		current.interval.cpuUsageMicros = uint64Pointer(delta)
	}
}

func calculateHostCurrent(sample hostmetrics.Sample, previous *previousHostSample, sampledAt time.Time) HostCurrent {
	current := HostCurrent{
		ObservedAt: sampledAt.UnixMilli(), CPUCores: sample.CPUCores,
		MemoryUsedBytes: sample.MemoryUsedBytes, MemoryPeakBytes: sample.MemoryUsedBytes,
		MemoryTotalBytes: sample.MemoryTotalBytes, NetworkInterface: sample.NetworkInterface,
	}
	if previous == nil {
		return current
	}
	elapsed := sampledAt.Sub(previous.at)
	current.interval.duration = elapsed
	prior := previous.sample
	if elapsed <= 0 || sample.CPUUnits < prior.CPUUnits || sample.CPUIdleUnits < prior.CPUIdleUnits || sample.CPUCores < 1 {
		return current
	}
	totalDelta := sample.CPUUnits - prior.CPUUnits
	idleDelta := sample.CPUIdleUnits - prior.CPUIdleUnits
	if totalDelta > 0 && idleDelta <= totalDelta {
		cpu := int64(math.Round(float64(totalDelta-idleDelta) * float64(sample.CPUCores*1000) / float64(totalDelta)))
		current.CPUMillicores = &cpu
		current.CPUPeakMillicores = int64Pointer(cpu)
	}
	if sample.NetworkRXBytes >= prior.NetworkRXBytes && sample.NetworkTXBytes >= prior.NetworkTXBytes {
		ingress := ratePerSecond(sample.NetworkRXBytes-prior.NetworkRXBytes, elapsed)
		egress := ratePerSecond(sample.NetworkTXBytes-prior.NetworkTXBytes, elapsed)
		current.NetworkIngressBytesPerSecond = &ingress
		current.NetworkIngressPeakBytesPerSecond = int64Pointer(ingress)
		current.NetworkEgressBytesPerSecond = &egress
		current.NetworkEgressPeakBytesPerSecond = int64Pointer(egress)
	}
	return current
}

func proxyMetricsFromCounters(counters trafficmetrics.Counters) *ProxyMetrics {
	return &ProxyMetrics{
		HTTP: HTTPMetrics{
			RequestsTotal: counters.HTTPRequestsTotal, ActiveRequests: max(counters.HTTPActiveRequests, 0),
			ActiveRequestsPeak: max(counters.HTTPActiveRequestsPeak, counters.HTTPActiveRequests, 0),
		},
		TCP: TCPMetrics{
			ConnectionsTotal: counters.TCPConnectionsTotal, ActiveConnections: max(counters.TCPActiveConnections, 0),
			ActiveConnectionsPeak: max(counters.TCPActiveConnectionsPeak, counters.TCPActiveConnections, 0),
		},
		UDP: UDPMetrics{IngressPacketsTotal: counters.UDPIngressPackets, EgressPacketsTotal: counters.UDPEgressPackets},
	}
}

func calculateProxyRates(current *Current, previous, counters trafficmetrics.Counters, elapsed time.Duration) {
	if current.Proxy == nil || elapsed <= 0 || !validProxyCounterDelta(previous, counters) {
		return
	}
	delta := &trafficDelta{
		httpRequests:   counters.HTTPRequestsTotal - previous.HTTPRequestsTotal,
		http2xx:        counters.HTTPResponses2xxTotal - previous.HTTPResponses2xxTotal,
		http3xx:        counters.HTTPResponses3xxTotal - previous.HTTPResponses3xxTotal,
		http4xx:        counters.HTTPResponses4xxTotal - previous.HTTPResponses4xxTotal,
		http5xx:        counters.HTTPResponses5xxTotal - previous.HTTPResponses5xxTotal,
		tcpConnections: counters.TCPConnectionsTotal - previous.TCPConnectionsTotal,
		udpIngress:     counters.UDPIngressPackets - previous.UDPIngressPackets,
		udpEgress:      counters.UDPEgressPackets - previous.UDPEgressPackets,
	}
	current.Proxy.HTTP.RequestsPerSecond = float64Pointer(rateFloat(delta.httpRequests, elapsed))
	httpPeak := *current.Proxy.HTTP.RequestsPerSecond
	tcpPeak := rateFloat(delta.tcpConnections, elapsed)
	udpIngressPeak := rateFloat(delta.udpIngress, elapsed)
	udpEgressPeak := rateFloat(delta.udpEgress, elapsed)
	if counters.ProtocolRatePeaksAvailable {
		httpPeak = math.Max(httpPeak, counters.HTTPRequestsPeakPerSecond)
		tcpPeak = math.Max(tcpPeak, counters.TCPConnectionsPeakPerSecond)
		udpIngressPeak = math.Max(udpIngressPeak, counters.UDPIngressPacketsPeakPerSecond)
		udpEgressPeak = math.Max(udpEgressPeak, counters.UDPEgressPacketsPeakPerSecond)
	}
	current.Proxy.HTTP.RequestsPeakPerSecond = float64Pointer(httpPeak)
	current.Proxy.HTTP.Responses2xxPerSecond = float64Pointer(rateFloat(delta.http2xx, elapsed))
	current.Proxy.HTTP.Responses3xxPerSecond = float64Pointer(rateFloat(delta.http3xx, elapsed))
	current.Proxy.HTTP.Responses4xxPerSecond = float64Pointer(rateFloat(delta.http4xx, elapsed))
	current.Proxy.HTTP.Responses5xxPerSecond = float64Pointer(rateFloat(delta.http5xx, elapsed))
	current.Proxy.TCP.ConnectionsPerSecond = float64Pointer(rateFloat(delta.tcpConnections, elapsed))
	current.Proxy.TCP.ConnectionsPeakPerSecond = float64Pointer(tcpPeak)
	current.Proxy.UDP.IngressPacketsPerSecond = float64Pointer(rateFloat(delta.udpIngress, elapsed))
	current.Proxy.UDP.IngressPacketsPeakPerSecond = float64Pointer(udpIngressPeak)
	current.Proxy.UDP.EgressPacketsPerSecond = float64Pointer(rateFloat(delta.udpEgress, elapsed))
	current.Proxy.UDP.EgressPacketsPeakPerSecond = float64Pointer(udpEgressPeak)
	for index := range current.httpDurationBuckets {
		delta.httpDurationBuckets[index] = counters.HTTPDurationBuckets[index] - previous.HTTPDurationBuckets[index]
		current.httpDurationBuckets[index] = delta.httpDurationBuckets[index]
	}
	current.interval.traffic = delta
	setLatencyPercentiles(&current.Proxy.HTTP, current.httpDurationBuckets)
}

func validProxyCounterDelta(previous, current trafficmetrics.Counters) bool {
	if current.HTTPRequestsTotal < previous.HTTPRequestsTotal ||
		current.HTTPResponses2xxTotal < previous.HTTPResponses2xxTotal ||
		current.HTTPResponses3xxTotal < previous.HTTPResponses3xxTotal ||
		current.HTTPResponses4xxTotal < previous.HTTPResponses4xxTotal ||
		current.HTTPResponses5xxTotal < previous.HTTPResponses5xxTotal ||
		current.TCPConnectionsTotal < previous.TCPConnectionsTotal ||
		current.UDPIngressPackets < previous.UDPIngressPackets || current.UDPEgressPackets < previous.UDPEgressPackets {
		return false
	}
	for index := range current.HTTPDurationBuckets {
		if current.HTTPDurationBuckets[index] < previous.HTTPDurationBuckets[index] {
			return false
		}
	}
	return true
}

func setLatencyPercentiles(metrics *HTTPMetrics, buckets [trafficmetrics.HTTPDurationBucketCount]uint64) {
	var total uint64
	for _, count := range buckets {
		total += count
	}
	if total == 0 {
		return
	}
	metrics.LatencyP50Millis = float64Pointer(latencyPercentile(buckets, total, 0.50))
	metrics.LatencyP95Millis = float64Pointer(latencyPercentile(buckets, total, 0.95))
	metrics.LatencyP99Millis = float64Pointer(latencyPercentile(buckets, total, 0.99))
}

func latencyPercentile(buckets [trafficmetrics.HTTPDurationBucketCount]uint64, total uint64, percentile float64) float64 {
	rank := uint64(math.Ceil(float64(total) * percentile))
	var cumulative uint64
	for index, count := range buckets {
		cumulative += count
		if cumulative < rank {
			continue
		}
		if index < len(trafficmetrics.HTTPDurationUpperBoundsMillis) {
			return float64(trafficmetrics.HTTPDurationUpperBoundsMillis[index])
		}
		return float64(trafficmetrics.HTTPDurationUpperBoundsMillis[len(trafficmetrics.HTTPDurationUpperBoundsMillis)-1])
	}
	return 0
}

func ratePerSecond(delta uint64, elapsed time.Duration) int64 {
	return int64(math.Round(float64(delta) * float64(time.Second) / float64(elapsed)))
}

func rateFloat(delta uint64, elapsed time.Duration) float64 {
	return float64(delta) * float64(time.Second) / float64(elapsed)
}

func newAggregateBuilder() *aggregateBuilder {
	return &aggregateBuilder{cpuComplete: true, networkComplete: true, proxyComplete: true}
}

func (builder *aggregateBuilder) add(current Current, publicService bool) {
	builder.current.ObservedAtMillis = max(builder.current.ObservedAtMillis, current.ObservedAtMillis)
	builder.current.interval.duration = max(builder.current.interval.duration, current.interval.duration)
	builder.current.HostCPUCores = max(builder.current.HostCPUCores, current.HostCPUCores)
	builder.current.HostMemoryBytes = max(builder.current.HostMemoryBytes, current.HostMemoryBytes)
	builder.current.MemoryBytes += current.MemoryBytes
	builder.current.MemoryPeakBytes += current.MemoryPeakBytes
	builder.current.TotalResources++
	if publicService {
		builder.publicServices++
		builder.addProxy(current)
		builder.current.NetworkRXBytes += current.NetworkRXBytes
		builder.current.NetworkTXBytes += current.NetworkTXBytes
		if current.NetworkIngressBytesPerSecond == nil || current.NetworkEgressBytesPerSecond == nil {
			builder.networkComplete = false
		} else {
			addInt(&builder.current.NetworkIngressBytesPerSecond, *current.NetworkIngressBytesPerSecond)
			addInt(&builder.current.NetworkEgressBytesPerSecond, *current.NetworkEgressBytesPerSecond)
			addUint(&builder.current.interval.networkIngressBytes, current.interval.networkIngressBytes)
			addUint(&builder.current.interval.networkEgressBytes, current.interval.networkEgressBytes)
		}
	}
	if !current.Running {
		return
	}
	builder.current.Running = true
	builder.current.RunningResources++
	if current.CPUMillicores == nil {
		builder.cpuComplete = false
	} else {
		addInt(&builder.current.CPUMillicores, *current.CPUMillicores)
		addUint(&builder.current.interval.cpuUsageMicros, current.interval.cpuUsageMicros)
	}
}

func (builder *aggregateBuilder) missing(publicService bool) {
	builder.current.TotalResources++
	builder.missingResources++
	builder.cpuComplete = false
	if publicService {
		builder.publicServices++
		builder.networkComplete = false
		builder.proxyComplete = false
	}
}

func (builder *aggregateBuilder) finish() Current {
	// Per-resource interval peaks are not necessarily concurrent. A scope with
	// multiple resources keeps the coincident sampled total to avoid inflation.
	if builder.current.TotalResources > 1 {
		builder.current.MemoryPeakBytes = builder.current.MemoryBytes
	}
	if builder.missingResources > 0 {
		builder.current.CPUMillicores = nil
	} else if builder.current.RunningResources == 0 {
		builder.current.CPUMillicores = int64Pointer(0)
	} else if !builder.cpuComplete {
		builder.current.CPUMillicores = nil
	}
	if builder.current.CPUMillicores != nil {
		builder.current.CPUPeakMillicores = int64Pointer(*builder.current.CPUMillicores)
	}
	if builder.publicServices == 0 {
		builder.current.NetworkIngressBytesPerSecond = int64Pointer(0)
		builder.current.NetworkEgressBytesPerSecond = int64Pointer(0)
		builder.current.NetworkAvailable = true
	} else if builder.networkComplete {
		builder.current.NetworkAvailable = true
	} else {
		builder.current.NetworkIngressBytesPerSecond = nil
		builder.current.NetworkEgressBytesPerSecond = nil
	}
	if builder.current.NetworkIngressBytesPerSecond != nil {
		builder.current.NetworkIngressPeakBytesPerSecond = int64Pointer(*builder.current.NetworkIngressBytesPerSecond)
		builder.current.NetworkEgressPeakBytesPerSecond = int64Pointer(*builder.current.NetworkEgressBytesPerSecond)
	}
	builder.finishProxy()
	return builder.current
}

func (builder *aggregateBuilder) addProxy(current Current) {
	if current.Proxy == nil {
		builder.proxyComplete = false
		return
	}
	if builder.current.Proxy == nil {
		builder.current.Proxy = &ProxyMetrics{}
	}
	target, source := builder.current.Proxy, current.Proxy
	target.HTTP.RequestsTotal += source.HTTP.RequestsTotal
	target.HTTP.ActiveRequests += source.HTTP.ActiveRequests
	target.HTTP.ActiveRequestsPeak += source.HTTP.ActiveRequestsPeak
	target.TCP.ConnectionsTotal += source.TCP.ConnectionsTotal
	target.TCP.ActiveConnections += source.TCP.ActiveConnections
	target.TCP.ActiveConnectionsPeak += source.TCP.ActiveConnectionsPeak
	target.UDP.IngressPacketsTotal += source.UDP.IngressPacketsTotal
	target.UDP.EgressPacketsTotal += source.UDP.EgressPacketsTotal
	if !addProxyRates(target, source) {
		builder.proxyComplete = false
	}
	if current.interval.traffic != nil {
		if builder.current.interval.traffic == nil {
			builder.current.interval.traffic = &trafficDelta{}
		}
		addTrafficDelta(builder.current.interval.traffic, current.interval.traffic)
	}
	for index, count := range current.httpDurationBuckets {
		builder.current.httpDurationBuckets[index] += count
	}
}

func addProxyRates(target, source *ProxyMetrics) bool {
	if source.HTTP.RequestsPerSecond == nil || source.HTTP.Responses2xxPerSecond == nil ||
		source.HTTP.RequestsPeakPerSecond == nil ||
		source.HTTP.Responses3xxPerSecond == nil || source.HTTP.Responses4xxPerSecond == nil ||
		source.HTTP.Responses5xxPerSecond == nil || source.TCP.ConnectionsPerSecond == nil ||
		source.TCP.ConnectionsPeakPerSecond == nil || source.UDP.IngressPacketsPerSecond == nil ||
		source.UDP.IngressPacketsPeakPerSecond == nil || source.UDP.EgressPacketsPerSecond == nil ||
		source.UDP.EgressPacketsPeakPerSecond == nil {
		return false
	}
	addFloat(&target.HTTP.RequestsPerSecond, *source.HTTP.RequestsPerSecond)
	addFloat(&target.HTTP.RequestsPeakPerSecond, *source.HTTP.RequestsPeakPerSecond)
	addFloat(&target.HTTP.Responses2xxPerSecond, *source.HTTP.Responses2xxPerSecond)
	addFloat(&target.HTTP.Responses3xxPerSecond, *source.HTTP.Responses3xxPerSecond)
	addFloat(&target.HTTP.Responses4xxPerSecond, *source.HTTP.Responses4xxPerSecond)
	addFloat(&target.HTTP.Responses5xxPerSecond, *source.HTTP.Responses5xxPerSecond)
	addFloat(&target.TCP.ConnectionsPerSecond, *source.TCP.ConnectionsPerSecond)
	addFloat(&target.TCP.ConnectionsPeakPerSecond, *source.TCP.ConnectionsPeakPerSecond)
	addFloat(&target.UDP.IngressPacketsPerSecond, *source.UDP.IngressPacketsPerSecond)
	addFloat(&target.UDP.IngressPacketsPeakPerSecond, *source.UDP.IngressPacketsPeakPerSecond)
	addFloat(&target.UDP.EgressPacketsPerSecond, *source.UDP.EgressPacketsPerSecond)
	addFloat(&target.UDP.EgressPacketsPeakPerSecond, *source.UDP.EgressPacketsPeakPerSecond)
	return true
}

func (builder *aggregateBuilder) finishProxy() {
	if builder.publicServices == 0 {
		builder.current.Proxy = zeroProxyMetrics()
		return
	}
	if builder.current.Proxy == nil {
		return
	}
	if !builder.proxyComplete {
		clearProxyRates(builder.current.Proxy)
		return
	}
	http, tcp, udp := &builder.current.Proxy.HTTP, &builder.current.Proxy.TCP, &builder.current.Proxy.UDP
	// Per-service peaks can occur in different one-second windows. Preserve the
	// exact peak for a single-service scope; for wider scopes use the coincident
	// collector rate instead of overstating it by summing unrelated peaks.
	if builder.publicServices > 1 {
		http.RequestsPeakPerSecond = cloneFloat(http.RequestsPerSecond)
		http.ActiveRequestsPeak = http.ActiveRequests
		tcp.ConnectionsPeakPerSecond = cloneFloat(tcp.ConnectionsPerSecond)
		tcp.ActiveConnectionsPeak = tcp.ActiveConnections
		udp.IngressPacketsPeakPerSecond = cloneFloat(udp.IngressPacketsPerSecond)
		udp.EgressPacketsPeakPerSecond = cloneFloat(udp.EgressPacketsPerSecond)
	}
	setLatencyPercentiles(http, builder.current.httpDurationBuckets)
}

func zeroProxyMetrics() *ProxyMetrics {
	return &ProxyMetrics{
		HTTP: HTTPMetrics{
			RequestsPerSecond: float64Pointer(0), RequestsPeakPerSecond: float64Pointer(0),
			Responses2xxPerSecond: float64Pointer(0), Responses3xxPerSecond: float64Pointer(0),
			Responses4xxPerSecond: float64Pointer(0), Responses5xxPerSecond: float64Pointer(0),
		},
		TCP: TCPMetrics{ConnectionsPerSecond: float64Pointer(0), ConnectionsPeakPerSecond: float64Pointer(0)},
		UDP: UDPMetrics{
			IngressPacketsPerSecond: float64Pointer(0), IngressPacketsPeakPerSecond: float64Pointer(0),
			EgressPacketsPerSecond: float64Pointer(0), EgressPacketsPeakPerSecond: float64Pointer(0),
		},
	}
}

func clearProxyRates(metrics *ProxyMetrics) {
	metrics.HTTP.RequestsPerSecond = nil
	metrics.HTTP.RequestsPeakPerSecond = nil
	metrics.HTTP.Responses2xxPerSecond = nil
	metrics.HTTP.Responses3xxPerSecond = nil
	metrics.HTTP.Responses4xxPerSecond = nil
	metrics.HTTP.Responses5xxPerSecond = nil
	metrics.HTTP.LatencyP50Millis = nil
	metrics.HTTP.LatencyP95Millis = nil
	metrics.HTTP.LatencyP99Millis = nil
	metrics.TCP.ConnectionsPerSecond = nil
	metrics.TCP.ConnectionsPeakPerSecond = nil
	metrics.UDP.IngressPacketsPerSecond = nil
	metrics.UDP.IngressPacketsPeakPerSecond = nil
	metrics.UDP.EgressPacketsPerSecond = nil
	metrics.UDP.EgressPacketsPeakPerSecond = nil
}

func addInt(target **int64, value int64) {
	if *target == nil {
		*target = int64Pointer(0)
	}
	**target += value
}

func addUint(target **uint64, value *uint64) {
	if value == nil {
		return
	}
	if *target == nil {
		*target = uint64Pointer(0)
	}
	**target += *value
}

func addTrafficDelta(target, source *trafficDelta) {
	target.httpRequests += source.httpRequests
	target.http2xx += source.http2xx
	target.http3xx += source.http3xx
	target.http4xx += source.http4xx
	target.http5xx += source.http5xx
	target.tcpConnections += source.tcpConnections
	target.udpIngress += source.udpIngress
	target.udpEgress += source.udpEgress
	for index, count := range source.httpDurationBuckets {
		target.httpDurationBuckets[index] += count
	}
}

func addFloat(target **float64, value float64) {
	if *target == nil {
		*target = float64Pointer(0)
	}
	**target += value
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return float64Pointer(*value)
}

func uint64Pointer(value uint64) *uint64    { return &value }
func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }
