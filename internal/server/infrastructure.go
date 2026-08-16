package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/diskusage"
	"github.com/iivankin/platformd/internal/journallogs"
	"github.com/iivankin/platformd/internal/resourcemetrics"
)

type DiskPressure interface {
	Snapshot() (diskpressure.Snapshot, bool)
}

type diskComponents interface {
	Components(context.Context) (diskusage.Snapshot, error)
}

type ResourceUsage interface {
	Read(cgroupstats.Kind, string) (resourcemetrics.Current, error)
	History(context.Context, cgroupstats.Kind, string, time.Duration) (resourcemetrics.History, error)
	ReadProject(string) (resourcemetrics.Current, error)
	ProjectHistory(context.Context, string, time.Duration) (resourcemetrics.History, error)
	ReadInstallation() (resourcemetrics.Current, error)
	InstallationHistory(context.Context, time.Duration) (resourcemetrics.History, error)
	HostHistory(context.Context, time.Duration) (resourcemetrics.History, error)
}

type InfrastructureLogs interface {
	Read(context.Context, journallogs.Query) (journallogs.Window, error)
}

type diskPressureResponse struct {
	Level               diskpressure.Level      `json:"level"`
	ByteBasisPoints     uint64                  `json:"byteBasisPoints"`
	InodeBasisPoints    uint64                  `json:"inodeBasisPoints"`
	TotalBytes          uint64                  `json:"totalBytes"`
	UsedBytes           uint64                  `json:"usedBytes"`
	AvailableBytes      uint64                  `json:"availableBytes"`
	TotalInodes         uint64                  `json:"totalInodes"`
	AvailableInodes     uint64                  `json:"availableInodes"`
	ReservePresent      bool                    `json:"reservePresent"`
	CheckedAt           int64                   `json:"checkedAt"`
	Components          []diskComponentResponse `json:"components"`
	ComponentsCheckedAt int64                   `json:"componentsCheckedAt,omitempty"`
}

type diskComponentResponse struct {
	ID     string `json:"id"`
	Bytes  uint64 `json:"bytes"`
	Parent string `json:"parent,omitempty"`
}

type resourceUsageResponse struct {
	ObservedAt                       int64                 `json:"observedAt"`
	MemoryBytes                      uint64                `json:"memoryBytes"`
	HostCPUCores                     int                   `json:"hostCpuCores"`
	HostMemoryBytes                  uint64                `json:"hostMemoryBytes"`
	NetworkAvailable                 bool                  `json:"networkAvailable"`
	Running                          bool                  `json:"running"`
	CPUMillicores                    *int64                `json:"cpuMillicores,omitempty"`
	CPUPeakMillicores                *int64                `json:"cpuPeakMillicores,omitempty"`
	MemoryPeakBytes                  uint64                `json:"memoryPeakBytes"`
	DiskBytes                        *uint64               `json:"diskBytes,omitempty"`
	NetworkIngressBytesPerSecond     *int64                `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkIngressPeakBytesPerSecond *int64                `json:"networkIngressPeakBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond      *int64                `json:"networkEgressBytesPerSecond,omitempty"`
	NetworkEgressPeakBytesPerSecond  *int64                `json:"networkEgressPeakBytesPerSecond,omitempty"`
	RunningResources                 int                   `json:"runningResources"`
	TotalResources                   int                   `json:"totalResources"`
	TrafficRoutes                    trafficRoutesResponse `json:"trafficRoutes"`
	Proxy                            *proxyUsageResponse   `json:"proxy,omitempty"`
	Host                             *hostUsageResponse    `json:"host,omitempty"`
}

type trafficRoutesResponse struct {
	HTTP bool `json:"http"`
	TCP  bool `json:"tcp"`
	UDP  bool `json:"udp"`
}

type proxyUsageResponse struct {
	HTTP httpProxyUsageResponse `json:"http"`
	TCP  tcpProxyUsageResponse  `json:"tcp"`
	UDP  udpProxyUsageResponse  `json:"udp"`
}

type httpProxyUsageResponse struct {
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

type tcpProxyUsageResponse struct {
	ConnectionsPerSecond     *float64 `json:"connectionsPerSecond,omitempty"`
	ConnectionsPeakPerSecond *float64 `json:"connectionsPeakPerSecond,omitempty"`
	ConnectionsTotal         uint64   `json:"connectionsTotal"`
	ActiveConnections        int64    `json:"activeConnections"`
	ActiveConnectionsPeak    int64    `json:"activeConnectionsPeak"`
}

type udpProxyUsageResponse struct {
	IngressPacketsPerSecond     *float64 `json:"ingressPacketsPerSecond,omitempty"`
	IngressPacketsPeakPerSecond *float64 `json:"ingressPacketsPeakPerSecond,omitempty"`
	EgressPacketsPerSecond      *float64 `json:"egressPacketsPerSecond,omitempty"`
	EgressPacketsPeakPerSecond  *float64 `json:"egressPacketsPeakPerSecond,omitempty"`
	IngressPacketsTotal         uint64   `json:"ingressPacketsTotal"`
	EgressPacketsTotal          uint64   `json:"egressPacketsTotal"`
}

type hostUsageResponse struct {
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

type resourceUsageHistoryPointResponse struct {
	ObservedAt                       int64               `json:"observedAt"`
	DurationMillis                   int64               `json:"durationMillis"`
	CPUMillicores                    *int64              `json:"cpuMillicores,omitempty"`
	CPUPeakMillicores                *int64              `json:"cpuPeakMillicores,omitempty"`
	MemoryBytes                      uint64              `json:"memoryBytes"`
	MemoryPeakBytes                  uint64              `json:"memoryPeakBytes"`
	DiskBytes                        *uint64             `json:"diskBytes,omitempty"`
	NetworkIngressBytesPerSecond     *int64              `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkIngressPeakBytesPerSecond *int64              `json:"networkIngressPeakBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond      *int64              `json:"networkEgressBytesPerSecond,omitempty"`
	NetworkEgressPeakBytesPerSecond  *int64              `json:"networkEgressPeakBytesPerSecond,omitempty"`
	Running                          bool                `json:"running"`
	Proxy                            *proxyUsageResponse `json:"proxy,omitempty"`
}

type resourceUsageHistoryResponse struct {
	From       int64                                `json:"from"`
	To         int64                                `json:"to"`
	StepMillis int64                                `json:"stepMillis"`
	Points     []resourceUsageHistoryPointResponse  `json:"points"`
	Series     []resourceUsageHistorySeriesResponse `json:"series"`
}

type resourceUsageHistorySeriesResponse struct {
	ID     string                              `json:"id"`
	Kind   string                              `json:"kind"`
	Name   string                              `json:"name"`
	Points []resourceUsageHistoryPointResponse `json:"points"`
}

func registerInfrastructureRoutes(
	mux *http.ServeMux,
	pressure DiskPressure,
	usage ResourceUsage,
	logs InfrastructureLogs,
) {
	if pressure != nil {
		mux.HandleFunc("GET /api/v1/infrastructure/disk-pressure", func(response http.ResponseWriter, request *http.Request) {
			if _, ok := access.IdentityFromContext(request.Context()); !ok {
				writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
				return
			}
			snapshot, ready := pressure.Snapshot()
			if !ready {
				writeAPIError(response, http.StatusServiceUnavailable, "disk_pressure_unavailable", "Disk pressure has not been measured")
				return
			}
			components := make([]diskComponentResponse, 0)
			var componentsCheckedAt int64
			if reader, ok := pressure.(diskComponents); ok {
				usage, err := reader.Components(request.Context())
				if err != nil {
					writeAPIError(response, http.StatusServiceUnavailable, "disk_usage_unavailable", "Disk component usage is unavailable")
					return
				}
				components = make([]diskComponentResponse, 0, len(usage.Components))
				for _, component := range usage.Components {
					components = append(components, diskComponentResponse{
						ID: component.ID, Bytes: component.Bytes, Parent: component.Parent,
					})
				}
				if !usage.CheckedAt.IsZero() {
					componentsCheckedAt = usage.CheckedAt.UnixMilli()
				}
			}
			writeJSON(response, http.StatusOK, diskPressureResponse{
				Level:           snapshot.Level,
				ByteBasisPoints: snapshot.Usage.ByteBasisPoints, InodeBasisPoints: snapshot.Usage.InodeBasisPoints,
				TotalBytes: snapshot.Usage.TotalBytes, UsedBytes: snapshot.Usage.UsedBytes,
				AvailableBytes: snapshot.Usage.AvailableBytes,
				TotalInodes:    snapshot.Usage.TotalInodes, AvailableInodes: snapshot.Usage.AvailableInodes,
				ReservePresent: snapshot.ReservePresent, CheckedAt: snapshot.CheckedAt.UnixMilli(),
				Components: components, ComponentsCheckedAt: componentsCheckedAt,
			})
		})
	}
	if usage != nil {
		mux.HandleFunc("GET /api/v1/infrastructure/resources/{kind}/{resourceID}/usage", resourceUsageHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/resources/{kind}/{resourceID}/usage/history", resourceUsageHistoryHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/projects/{projectID}/usage", projectUsageHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/projects/{projectID}/usage/history", projectUsageHistoryHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/usage", installationUsageHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/usage/history", installationUsageHistoryHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/host/usage/history", hostUsageHistoryHandler(usage))
	}
	if logs != nil {
		mux.HandleFunc("GET /api/v1/infrastructure/logs", infrastructureLogsHandler(logs))
	}
}

func resourceUsageHandler(usage ResourceUsage) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		sample, err := usage.Read(cgroupstats.Kind(request.PathValue("kind")), request.PathValue("resourceID"))
		if errors.Is(err, cgroupstats.ErrInvalidResource) {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage", err.Error())
			return
		}
		if errors.Is(err, resourcemetrics.ErrNotReady) {
			writeAPIError(response, http.StatusServiceUnavailable, "resource_usage_warming_up", "Resource usage is warming up")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "resource_usage_unavailable", "Resource usage is unavailable")
			return
		}
		writeJSON(response, http.StatusOK, resourceUsageResponseFor(sample))
	}
}

func projectUsageHandler(usage ResourceUsage) http.HandlerFunc {
	return currentUsageHandler(func(request *http.Request) (resourcemetrics.Current, error) {
		return usage.ReadProject(request.PathValue("projectID"))
	})
}

func installationUsageHandler(usage ResourceUsage) http.HandlerFunc {
	return currentUsageHandler(func(*http.Request) (resourcemetrics.Current, error) {
		return usage.ReadInstallation()
	})
}

func currentUsageHandler(read func(*http.Request) (resourcemetrics.Current, error)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		current, err := read(request)
		if errors.Is(err, cgroupstats.ErrInvalidResource) {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage", err.Error())
			return
		}
		if errors.Is(err, resourcemetrics.ErrNotReady) {
			writeAPIError(response, http.StatusServiceUnavailable, "resource_usage_warming_up", "Resource usage is warming up")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "resource_usage_unavailable", "Resource usage is unavailable")
			return
		}
		writeJSON(response, http.StatusOK, resourceUsageResponseFor(current))
	}
}

func resourceUsageResponseFor(sample resourcemetrics.Current) resourceUsageResponse {
	return resourceUsageResponse{
		ObservedAt:  sample.ObservedAtMillis,
		MemoryBytes: sample.MemoryBytes, HostCPUCores: sample.HostCPUCores,
		HostMemoryBytes: sample.HostMemoryBytes, NetworkAvailable: sample.NetworkAvailable,
		Running: sample.Running, CPUMillicores: sample.CPUMillicores, CPUPeakMillicores: sample.CPUPeakMillicores,
		MemoryPeakBytes:                  sample.MemoryPeakBytes,
		DiskBytes:                        sample.DiskBytes,
		NetworkIngressBytesPerSecond:     sample.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: sample.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      sample.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  sample.NetworkEgressPeakBytesPerSecond,
		RunningResources:                 sample.RunningResources, TotalResources: sample.TotalResources,
		TrafficRoutes: trafficRoutesResponse{
			HTTP: sample.TrafficRoutes.HTTP, TCP: sample.TrafficRoutes.TCP, UDP: sample.TrafficRoutes.UDP,
		},
		Proxy: proxyUsageResponseFor(sample.Proxy), Host: hostUsageResponseFor(sample.Host),
	}
}

func proxyUsageResponseFor(metrics *resourcemetrics.ProxyMetrics) *proxyUsageResponse {
	if metrics == nil {
		return nil
	}
	return &proxyUsageResponse{
		HTTP: httpProxyUsageResponse{
			RequestsPerSecond: metrics.HTTP.RequestsPerSecond, RequestsPeakPerSecond: metrics.HTTP.RequestsPeakPerSecond,
			RequestsTotal: metrics.HTTP.RequestsTotal, ActiveRequests: metrics.HTTP.ActiveRequests,
			ActiveRequestsPeak:    metrics.HTTP.ActiveRequestsPeak,
			Responses2xxPerSecond: metrics.HTTP.Responses2xxPerSecond,
			Responses3xxPerSecond: metrics.HTTP.Responses3xxPerSecond, Responses4xxPerSecond: metrics.HTTP.Responses4xxPerSecond,
			Responses5xxPerSecond: metrics.HTTP.Responses5xxPerSecond, LatencyP50Millis: metrics.HTTP.LatencyP50Millis,
			LatencyP95Millis: metrics.HTTP.LatencyP95Millis, LatencyP99Millis: metrics.HTTP.LatencyP99Millis,
		},
		TCP: tcpProxyUsageResponse{
			ConnectionsPerSecond:     metrics.TCP.ConnectionsPerSecond,
			ConnectionsPeakPerSecond: metrics.TCP.ConnectionsPeakPerSecond,
			ConnectionsTotal:         metrics.TCP.ConnectionsTotal, ActiveConnections: metrics.TCP.ActiveConnections,
			ActiveConnectionsPeak: metrics.TCP.ActiveConnectionsPeak,
		},
		UDP: udpProxyUsageResponse{
			IngressPacketsPerSecond:     metrics.UDP.IngressPacketsPerSecond,
			IngressPacketsPeakPerSecond: metrics.UDP.IngressPacketsPeakPerSecond,
			EgressPacketsPerSecond:      metrics.UDP.EgressPacketsPerSecond,
			EgressPacketsPeakPerSecond:  metrics.UDP.EgressPacketsPeakPerSecond,
			IngressPacketsTotal:         metrics.UDP.IngressPacketsTotal, EgressPacketsTotal: metrics.UDP.EgressPacketsTotal,
		},
	}
}

func hostUsageResponseFor(host *resourcemetrics.HostCurrent) *hostUsageResponse {
	if host == nil {
		return nil
	}
	return &hostUsageResponse{
		ObservedAt: host.ObservedAt, CPUMillicores: host.CPUMillicores, CPUPeakMillicores: host.CPUPeakMillicores,
		CPUCores: host.CPUCores, MemoryUsedBytes: host.MemoryUsedBytes, MemoryPeakBytes: host.MemoryPeakBytes,
		MemoryTotalBytes:                 host.MemoryTotalBytes,
		NetworkIngressBytesPerSecond:     host.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: host.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      host.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  host.NetworkEgressPeakBytesPerSecond,
		NetworkInterface:                 host.NetworkInterface,
	}
}

func resourceUsageHistoryHandler(usage ResourceUsage) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		window, ok := resourceMetricWindows[request.URL.Query().Get("range")]
		if !ok {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage_range", "range must be one of 1h, 6h, 1d, 7d, or 30d")
			return
		}
		history, err := usage.History(
			request.Context(), cgroupstats.Kind(request.PathValue("kind")), request.PathValue("resourceID"), window,
		)
		if errors.Is(err, cgroupstats.ErrInvalidResource) || errors.Is(err, resourcemetrics.ErrInvalidRange) {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage", err.Error())
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "resource_usage_unavailable", "Resource usage is unavailable")
			return
		}
		writeResourceUsageHistory(response, history)
	}
}

func projectUsageHistoryHandler(usage ResourceUsage) http.HandlerFunc {
	return usageHistoryHandler(func(request *http.Request, window time.Duration) (resourcemetrics.History, error) {
		return usage.ProjectHistory(request.Context(), request.PathValue("projectID"), window)
	})
}

func installationUsageHistoryHandler(usage ResourceUsage) http.HandlerFunc {
	return usageHistoryHandler(func(request *http.Request, window time.Duration) (resourcemetrics.History, error) {
		return usage.InstallationHistory(request.Context(), window)
	})
}

func hostUsageHistoryHandler(usage ResourceUsage) http.HandlerFunc {
	return usageHistoryHandler(func(request *http.Request, window time.Duration) (resourcemetrics.History, error) {
		return usage.HostHistory(request.Context(), window)
	})
}

func usageHistoryHandler(read func(*http.Request, time.Duration) (resourcemetrics.History, error)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		window, ok := resourceMetricWindows[request.URL.Query().Get("range")]
		if !ok {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage_range", "range must be one of 1h, 6h, 1d, 7d, or 30d")
			return
		}
		history, err := read(request, window)
		if errors.Is(err, cgroupstats.ErrInvalidResource) || errors.Is(err, resourcemetrics.ErrInvalidRange) {
			writeAPIError(response, http.StatusBadRequest, "invalid_resource_usage", err.Error())
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "resource_usage_unavailable", "Resource usage is unavailable")
			return
		}
		writeResourceUsageHistory(response, history)
	}
}

func writeResourceUsageHistory(response http.ResponseWriter, history resourcemetrics.History) {
	points := make([]resourceUsageHistoryPointResponse, 0, len(history.Points))
	for _, point := range history.Points {
		points = append(points, resourceUsageHistoryPointResponseFor(point))
	}
	series := make([]resourceUsageHistorySeriesResponse, 0, len(history.Series))
	for _, item := range history.Series {
		seriesPoints := make([]resourceUsageHistoryPointResponse, 0, len(item.Points))
		for _, point := range item.Points {
			seriesPoints = append(seriesPoints, resourceUsageHistoryPointResponseFor(point))
		}
		series = append(series, resourceUsageHistorySeriesResponse{
			ID: item.ID, Kind: item.Kind, Name: item.Name, Points: seriesPoints,
		})
	}
	writeJSON(response, http.StatusOK, resourceUsageHistoryResponse{
		From: history.From, To: history.To, StepMillis: history.StepMillis, Points: points, Series: series,
	})
}

func resourceUsageHistoryPointResponseFor(point resourcemetrics.Point) resourceUsageHistoryPointResponse {
	return resourceUsageHistoryPointResponse{
		ObservedAt: point.ObservedAt, DurationMillis: point.DurationMillis,
		CPUMillicores: point.CPUMillicores, CPUPeakMillicores: point.CPUPeakMillicores,
		MemoryBytes: point.MemoryBytes, MemoryPeakBytes: point.MemoryPeakBytes, DiskBytes: point.DiskBytes,
		NetworkIngressBytesPerSecond:     point.NetworkIngressBytesPerSecond,
		NetworkIngressPeakBytesPerSecond: point.NetworkIngressPeakBytesPerSecond,
		NetworkEgressBytesPerSecond:      point.NetworkEgressBytesPerSecond,
		NetworkEgressPeakBytesPerSecond:  point.NetworkEgressPeakBytesPerSecond,
		Running:                          point.Running, Proxy: proxyUsageResponseFor(point.Proxy),
	}
}

var resourceMetricWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"1d":  24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

func infrastructureLogsHandler(logs InfrastructureLogs) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		limit := journallogs.DefaultLimit
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > journallogs.MaximumLimit {
				writeAPIError(response, http.StatusBadRequest, "invalid_journal_query", "limit must be an integer from 1 to 2000")
				return
			}
			limit = parsed
		}
		window, err := logs.Read(request.Context(), journallogs.Query{
			Limit: limit, BeforeCursor: request.URL.Query().Get("beforeCursor"),
		})
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, window)
		case errors.Is(err, journallogs.ErrInvalidQuery):
			writeAPIError(response, http.StatusBadRequest, "invalid_journal_query", err.Error())
		default:
			writeAPIError(response, http.StatusServiceUnavailable, "journal_unavailable", "Unable to read system journal", err)
		}
	}
}
