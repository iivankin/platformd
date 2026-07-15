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
	"github.com/iivankin/platformd/internal/journallogs"
	"github.com/iivankin/platformd/internal/resourcemetrics"
)

type DiskPressure interface {
	Snapshot() (diskpressure.Snapshot, bool)
}

type ResourceUsage interface {
	Read(cgroupstats.Kind, string) (resourcemetrics.Current, error)
	History(context.Context, cgroupstats.Kind, string, time.Duration) (resourcemetrics.History, error)
}

type InfrastructureLogs interface {
	Read(context.Context, journallogs.Query) (journallogs.Window, error)
}

type diskPressureResponse struct {
	Level            diskpressure.Level `json:"level"`
	ByteBasisPoints  uint64             `json:"byteBasisPoints"`
	InodeBasisPoints uint64             `json:"inodeBasisPoints"`
	TotalBytes       uint64             `json:"totalBytes"`
	AvailableBytes   uint64             `json:"availableBytes"`
	TotalInodes      uint64             `json:"totalInodes"`
	AvailableInodes  uint64             `json:"availableInodes"`
	ReservePresent   bool               `json:"reservePresent"`
	CheckedAt        int64              `json:"checkedAt"`
}

type resourceUsageResponse struct {
	ObservedAt       int64  `json:"observedAt"`
	CPUUsageMicros   uint64 `json:"cpuUsageMicros"`
	MemoryBytes      uint64 `json:"memoryBytes"`
	HostCPUCores     int    `json:"hostCpuCores"`
	HostMemoryBytes  uint64 `json:"hostMemoryBytes"`
	NetworkRXBytes   uint64 `json:"networkRxBytes"`
	NetworkTXBytes   uint64 `json:"networkTxBytes"`
	NetworkAvailable bool   `json:"networkAvailable"`
	Running          bool   `json:"running"`
}

type resourceUsageHistoryPointResponse struct {
	ObservedAt                   int64  `json:"observedAt"`
	CPUMillicores                *int64 `json:"cpuMillicores,omitempty"`
	MemoryBytes                  uint64 `json:"memoryBytes"`
	NetworkIngressBytesPerSecond *int64 `json:"networkIngressBytesPerSecond,omitempty"`
	NetworkEgressBytesPerSecond  *int64 `json:"networkEgressBytesPerSecond,omitempty"`
	Running                      bool   `json:"running"`
}

type resourceUsageHistoryResponse struct {
	From       int64                               `json:"from"`
	To         int64                               `json:"to"`
	StepMillis int64                               `json:"stepMillis"`
	Points     []resourceUsageHistoryPointResponse `json:"points"`
}

func registerInfrastructureRoutes(mux *http.ServeMux, pressure DiskPressure, usage ResourceUsage, logs InfrastructureLogs) {
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
			writeJSON(response, http.StatusOK, diskPressureResponse{
				Level:           snapshot.Level,
				ByteBasisPoints: snapshot.Usage.ByteBasisPoints, InodeBasisPoints: snapshot.Usage.InodeBasisPoints,
				TotalBytes: snapshot.Usage.TotalBytes, AvailableBytes: snapshot.Usage.AvailableBytes,
				TotalInodes: snapshot.Usage.TotalInodes, AvailableInodes: snapshot.Usage.AvailableInodes,
				ReservePresent: snapshot.ReservePresent, CheckedAt: snapshot.CheckedAt.UnixMilli(),
			})
		})
	}
	if usage != nil {
		mux.HandleFunc("GET /api/v1/infrastructure/resources/{kind}/{resourceID}/usage", resourceUsageHandler(usage))
		mux.HandleFunc("GET /api/v1/infrastructure/resources/{kind}/{resourceID}/usage/history", resourceUsageHistoryHandler(usage))
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
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "resource_usage_unavailable", "Resource usage is unavailable")
			return
		}
		writeJSON(response, http.StatusOK, resourceUsageResponse{
			ObservedAt: sample.ObservedAtMillis, CPUUsageMicros: sample.CPUUsageMicros,
			MemoryBytes: sample.MemoryBytes, HostCPUCores: sample.HostCPUCores,
			HostMemoryBytes: sample.HostMemoryBytes, NetworkRXBytes: sample.NetworkRXBytes,
			NetworkTXBytes: sample.NetworkTXBytes, NetworkAvailable: sample.NetworkAvailable,
			Running: sample.Running,
		})
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
		points := make([]resourceUsageHistoryPointResponse, 0, len(history.Points))
		for _, point := range history.Points {
			points = append(points, resourceUsageHistoryPointResponse{
				ObservedAt: point.ObservedAt, CPUMillicores: point.CPUMillicores,
				MemoryBytes:                  point.MemoryBytes,
				NetworkIngressBytesPerSecond: point.NetworkIngressBytesPerSecond,
				NetworkEgressBytesPerSecond:  point.NetworkEgressBytesPerSecond,
				Running:                      point.Running,
			})
		}
		writeJSON(response, http.StatusOK, resourceUsageHistoryResponse{
			From: history.From, To: history.To, StepMillis: history.StepMillis, Points: points,
		})
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
		window, err := logs.Read(request.Context(), journallogs.Query{Limit: limit})
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, window)
		case errors.Is(err, journallogs.ErrInvalidQuery):
			writeAPIError(response, http.StatusBadRequest, "invalid_journal_query", err.Error())
		default:
			writeAPIError(response, http.StatusServiceUnavailable, "journal_unavailable", "Unable to read platform journal")
		}
	}
}
