package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type LogRepository interface {
	ServiceLogs(context.Context, string, string, string, string, int) (containerlogs.Window, error)
}

func registerLogRoutes(mux *http.ServeMux, repository LogRepository) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs", getServiceLogs(repository))
}

func getServiceLogs(repository LogRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		limit := 0
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > containerlogs.MaximumLimit {
				writeAPIError(response, http.StatusBadRequest, "invalid_log_limit", "limit must be an integer from 1 to 2000")
				return
			}
			limit = parsed
		}
		window, err := repository.ServiceLogs(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"),
			request.URL.Query().Get("deploymentId"), request.URL.Query().Get("contains"), limit,
		)
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, window)
		case errors.Is(err, state.ErrServiceNotFound):
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
		case errors.Is(err, containerlogs.ErrInvalidQuery):
			writeAPIError(response, http.StatusBadRequest, "invalid_log_query", err.Error())
		default:
			writeAPIError(response, http.StatusInternalServerError, "log_read_failed", "Unable to read service logs")
		}
	}
}
