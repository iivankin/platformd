package server

import (
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/diskpressure"
)

type DiskPressure interface {
	Snapshot() (diskpressure.Snapshot, bool)
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

func registerInfrastructureRoutes(mux *http.ServeMux, pressure DiskPressure) {
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
