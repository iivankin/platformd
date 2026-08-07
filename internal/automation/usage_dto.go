package automation

import "github.com/iivankin/platformd/internal/resourcemetrics"

type UsageSnapshot struct {
	ObservedAt                       int64              `json:"observedAt"`
	MemoryBytes                      uint64             `json:"memoryBytes"`
	HostCPUCores                     int                `json:"hostCpuCores"`
	HostMemoryBytes                  uint64             `json:"hostMemoryBytes"`
	NetworkAvailable                 bool               `json:"networkAvailable"`
	Running                          bool               `json:"running"`
	CPUMillicores                    *int64             `json:"cpuMillicores,omitempty"`
	CPUPeakMillicores                *int64             `json:"cpuPeakMillicores,omitempty"`
	MemoryPeakBytes                  uint64             `json:"memoryPeakBytes"`
	DiskBytes                        *uint64            `json:"diskBytes,omitempty"`
	NetworkIngressBytesPerSecond     *int64             `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkIngressPeakBytesPerSecond *int64             `json:"networkIngressPeakBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond      *int64             `json:"networkEgressBytesPerSecond,omitempty"`
	NetworkEgressPeakBytesPerSecond  *int64             `json:"networkEgressPeakBytesPerSecond,omitempty"`
	RunningResources                 int                `json:"runningResources"`
	TotalResources                   int                `json:"totalResources"`
	TrafficRoutes                    UsageTrafficRoutes `json:"trafficRoutes"`
	Proxy                            *UsageProxy        `json:"proxy,omitempty"`
	Host                             *UsageHost         `json:"host,omitempty"`
}

type UsageTrafficRoutes struct {
	HTTP bool `json:"http"`
	TCP  bool `json:"tcp"`
	UDP  bool `json:"udp"`
}

type UsageProxy struct {
	HTTP UsageHTTPProxy `json:"http"`
	TCP  UsageTCPProxy  `json:"tcp"`
	UDP  UsageUDPProxy  `json:"udp"`
}

type UsageHTTPProxy struct {
	RequestsPerSecond     *float64 `json:"requestsPerSecond,omitempty"`
	RequestsPeakPerSecond *float64 `json:"requestsPeakPerSecond,omitempty"`
	RequestsTotal         uint64   `json:"requestsTotal"`
	ActiveRequests        int64    `json:"activeRequests"`
	ActiveRequestsPeak    int64    `json:"activeRequestsPeak"`
	Responses2xxPerSecond *float64 `json:"responses2xxPerSecond,omitempty"`
	Responses3xxPerSecond *float64 `json:"responses3xxPerSecond,omitempty"`
	Responses4xxPerSecond *float64 `json:"responses4xxPerSecond,omitempty"`
	Responses5xxPerSecond *float64 `json:"responses5xxPerSecond,omitempty"`
	LatencyP50Millis      *float64 `json:"latencyP50Millis,omitempty"`
	LatencyP95Millis      *float64 `json:"latencyP95Millis,omitempty"`
	LatencyP99Millis      *float64 `json:"latencyP99Millis,omitempty"`
}

type UsageTCPProxy struct {
	ConnectionsPerSecond     *float64 `json:"connectionsPerSecond,omitempty"`
	ConnectionsPeakPerSecond *float64 `json:"connectionsPeakPerSecond,omitempty"`
	ConnectionsTotal         uint64   `json:"connectionsTotal"`
	ActiveConnections        int64    `json:"activeConnections"`
	ActiveConnectionsPeak    int64    `json:"activeConnectionsPeak"`
}

type UsageUDPProxy struct {
	IngressPacketsPerSecond     *float64 `json:"ingressPacketsPerSecond,omitempty"`
	IngressPacketsPeakPerSecond *float64 `json:"ingressPacketsPeakPerSecond,omitempty"`
	EgressPacketsPerSecond      *float64 `json:"egressPacketsPerSecond,omitempty"`
	EgressPacketsPeakPerSecond  *float64 `json:"egressPacketsPeakPerSecond,omitempty"`
	IngressPacketsTotal         uint64   `json:"ingressPacketsTotal"`
	EgressPacketsTotal          uint64   `json:"egressPacketsTotal"`
}

type UsageHost struct {
	ObservedAt                       int64  `json:"observedAt"`
	CPUMillicores                    *int64 `json:"cpuMillicores,omitempty"`
	CPUPeakMillicores                *int64 `json:"cpuPeakMillicores,omitempty"`
	CPUCores                         int    `json:"cpuCores"`
	MemoryUsedBytes                  uint64 `json:"memoryUsedBytes"`
	MemoryPeakBytes                  uint64 `json:"memoryPeakBytes"`
	MemoryTotalBytes                 uint64 `json:"memoryTotalBytes"`
	NetworkIngressBytesPerSecond     *int64 `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkIngressPeakBytesPerSecond *int64 `json:"networkIngressPeakBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond      *int64 `json:"networkEgressBytesPerSecond,omitempty"`
	NetworkEgressPeakBytesPerSecond  *int64 `json:"networkEgressPeakBytesPerSecond,omitempty"`
	NetworkInterface                 string `json:"networkInterface"`
}

type UsageHistory struct {
	From       int64                `json:"from"`
	To         int64                `json:"to"`
	StepMillis int64                `json:"stepMillis"`
	Points     []UsageHistoryPoint  `json:"points"`
	Series     []UsageHistorySeries `json:"series"`
}

type UsageHistoryPoint struct {
	ObservedAt                       int64       `json:"observedAt"`
	DurationMillis                   int64       `json:"durationMillis"`
	CPUMillicores                    *int64      `json:"cpuMillicores,omitempty"`
	CPUPeakMillicores                *int64      `json:"cpuPeakMillicores,omitempty"`
	MemoryBytes                      uint64      `json:"memoryBytes"`
	MemoryPeakBytes                  uint64      `json:"memoryPeakBytes"`
	DiskBytes                        *uint64     `json:"diskBytes,omitempty"`
	NetworkIngressBytesPerSecond     *int64      `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkIngressPeakBytesPerSecond *int64      `json:"networkIngressPeakBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond      *int64      `json:"networkEgressBytesPerSecond,omitempty"`
	NetworkEgressPeakBytesPerSecond  *int64      `json:"networkEgressPeakBytesPerSecond,omitempty"`
	Running                          bool        `json:"running"`
	Proxy                            *UsageProxy `json:"proxy,omitempty"`
}

type UsageHistorySeries struct {
	ID     string              `json:"id"`
	Kind   string              `json:"kind"`
	Name   string              `json:"name"`
	Points []UsageHistoryPoint `json:"points"`
}

func usageSnapshotFor(sample resourcemetrics.Current) UsageSnapshot {
	return UsageSnapshot{
		ObservedAt: sample.ObservedAtMillis, MemoryBytes: sample.MemoryBytes,
		HostCPUCores: sample.HostCPUCores, HostMemoryBytes: sample.HostMemoryBytes,
		NetworkAvailable: sample.NetworkAvailable, Running: sample.Running,
		CPUMillicores: sample.CPUMillicores, CPUPeakMillicores: sample.CPUPeakMillicores,
		MemoryPeakBytes: sample.MemoryPeakBytes, DiskBytes: sample.DiskBytes,
		NetworkIngressBytesPerSecond:     sample.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: sample.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      sample.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  sample.NetworkEgressPeakBytesPerSecond,
		RunningResources:                 sample.RunningResources, TotalResources: sample.TotalResources,
		TrafficRoutes: UsageTrafficRoutes{
			HTTP: sample.TrafficRoutes.HTTP, TCP: sample.TrafficRoutes.TCP, UDP: sample.TrafficRoutes.UDP,
		},
		Proxy: usageProxyFor(sample.Proxy), Host: usageHostFor(sample.Host),
	}
}

func usageHistoryFor(history resourcemetrics.History) UsageHistory {
	points := make([]UsageHistoryPoint, 0, len(history.Points))
	for _, point := range history.Points {
		points = append(points, usageHistoryPointFor(point))
	}
	series := make([]UsageHistorySeries, 0, len(history.Series))
	for _, item := range history.Series {
		seriesPoints := make([]UsageHistoryPoint, 0, len(item.Points))
		for _, point := range item.Points {
			seriesPoints = append(seriesPoints, usageHistoryPointFor(point))
		}
		series = append(series, UsageHistorySeries{
			ID: item.ID, Kind: item.Kind, Name: item.Name, Points: seriesPoints,
		})
	}
	return UsageHistory{
		From: history.From, To: history.To, StepMillis: history.StepMillis,
		Points: points, Series: series,
	}
}

func usageHistoryPointFor(point resourcemetrics.Point) UsageHistoryPoint {
	return UsageHistoryPoint{
		ObservedAt: point.ObservedAt, DurationMillis: point.DurationMillis,
		CPUMillicores: point.CPUMillicores, CPUPeakMillicores: point.CPUPeakMillicores,
		MemoryBytes: point.MemoryBytes, MemoryPeakBytes: point.MemoryPeakBytes, DiskBytes: point.DiskBytes,
		NetworkIngressBytesPerSecond:     point.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: point.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      point.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  point.NetworkEgressPeakBytesPerSecond,
		Running:                          point.Running, Proxy: usageProxyFor(point.Proxy),
	}
}

func usageProxyFor(metrics *resourcemetrics.ProxyMetrics) *UsageProxy {
	if metrics == nil {
		return nil
	}
	return &UsageProxy{
		HTTP: UsageHTTPProxy{
			RequestsPerSecond: metrics.HTTP.RequestsPerSecond, RequestsPeakPerSecond: metrics.HTTP.RequestsPeakPerSecond,
			RequestsTotal: metrics.HTTP.RequestsTotal, ActiveRequests: metrics.HTTP.ActiveRequests,
			ActiveRequestsPeak: metrics.HTTP.ActiveRequestsPeak, Responses2xxPerSecond: metrics.HTTP.Responses2xxPerSecond,
			Responses3xxPerSecond: metrics.HTTP.Responses3xxPerSecond, Responses4xxPerSecond: metrics.HTTP.Responses4xxPerSecond,
			Responses5xxPerSecond: metrics.HTTP.Responses5xxPerSecond, LatencyP50Millis: metrics.HTTP.LatencyP50Millis,
			LatencyP95Millis: metrics.HTTP.LatencyP95Millis, LatencyP99Millis: metrics.HTTP.LatencyP99Millis,
		},
		TCP: UsageTCPProxy{
			ConnectionsPerSecond: metrics.TCP.ConnectionsPerSecond, ConnectionsPeakPerSecond: metrics.TCP.ConnectionsPeakPerSecond,
			ConnectionsTotal: metrics.TCP.ConnectionsTotal, ActiveConnections: metrics.TCP.ActiveConnections,
			ActiveConnectionsPeak: metrics.TCP.ActiveConnectionsPeak,
		},
		UDP: UsageUDPProxy{
			IngressPacketsPerSecond: metrics.UDP.IngressPacketsPerSecond, IngressPacketsPeakPerSecond: metrics.UDP.IngressPacketsPeakPerSecond,
			EgressPacketsPerSecond: metrics.UDP.EgressPacketsPerSecond, EgressPacketsPeakPerSecond: metrics.UDP.EgressPacketsPeakPerSecond,
			IngressPacketsTotal: metrics.UDP.IngressPacketsTotal, EgressPacketsTotal: metrics.UDP.EgressPacketsTotal,
		},
	}
}

func usageHostFor(host *resourcemetrics.HostCurrent) *UsageHost {
	if host == nil {
		return nil
	}
	return &UsageHost{
		ObservedAt: host.ObservedAt, CPUMillicores: host.CPUMillicores, CPUPeakMillicores: host.CPUPeakMillicores,
		CPUCores: host.CPUCores, MemoryUsedBytes: host.MemoryUsedBytes, MemoryPeakBytes: host.MemoryPeakBytes,
		MemoryTotalBytes: host.MemoryTotalBytes, NetworkIngressBytesPerSecond: host.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: host.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      host.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  host.NetworkEgressPeakBytesPerSecond,
		NetworkInterface:                 host.NetworkInterface,
	}
}
