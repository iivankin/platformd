package server

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/diskpressure"
)

type DiskPressure interface {
	Snapshot() (diskpressure.Snapshot, bool)
}

type ResourceUsage interface {
	Read(cgroupstats.Kind, string) (cgroupstats.Sample, error)
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
	ObservedAt      int64  `json:"observedAt"`
	CPUUsageMicros  uint64 `json:"cpuUsageMicros"`
	MemoryBytes     uint64 `json:"memoryBytes"`
	HostCPUCores    int    `json:"hostCpuCores"`
	HostMemoryBytes uint64 `json:"hostMemoryBytes"`
	Running         bool   `json:"running"`
}

func registerInfrastructureRoutes(mux *http.ServeMux, pressure DiskPressure, usage ResourceUsage) {
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
	if usage == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/infrastructure/resources/{kind}/{resourceID}/usage", func(response http.ResponseWriter, request *http.Request) {
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
			HostMemoryBytes: sample.HostMemoryBytes, Running: sample.Running,
		})
	})
}
