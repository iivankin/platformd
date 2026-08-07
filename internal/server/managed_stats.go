package server

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/managedstats"
)

func writeManagedStatsError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, managedstats.ErrInvalidRange):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_stats_range", "range must be one of 1h, 6h, 1d, 7d, or 30d")
	case errors.Is(err, managedstats.ErrInvalidKind), errors.Is(err, managedstats.ErrInvalidTarget):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_stats", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "managed_stats_unavailable", "Managed stats history is unavailable", err)
	}
	return true
}
