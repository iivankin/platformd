package automationapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

func readServiceLogs(application *automation.LogApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		if !identity.AllowsProject(projectID) {
			writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
			return
		}
		limit, ok := logLimit(response, request)
		if !ok {
			return
		}
		window, err := application.ReadService(request.Context(), identity, automation.ReadServiceLogsInput{
			ProjectID: projectID, ServiceID: request.PathValue("serviceID"),
			DeploymentID: request.URL.Query().Get("deploymentId"),
			Contains:     request.URL.Query().Get("contains"), Limit: limit,
		})
		if writeLogReadError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, window)
	}
}

func logLimit(response http.ResponseWriter, request *http.Request) (int, bool) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > containerlogs.MaximumLimit {
		writeError(response, http.StatusBadRequest, "invalid_log_limit", "limit must be an integer from 1 to 2000")
		return 0, false
	}
	return limit, true
}

func writeLogReadError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, automation.ErrReadTokenRequired):
		writeError(response, http.StatusForbidden, "read_token_required", "A read or admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, state.ErrServiceNotFound):
		writeError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, containerlogs.ErrInvalidQuery):
		writeError(response, http.StatusBadRequest, "invalid_log_query", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "log_read_failed", "Unable to read service logs")
	}
	return true
}
