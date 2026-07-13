package automationapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

func listManagedResources(application *automation.ManagedResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		resources, err := application.List(request.Context(), identity, request.PathValue("projectID"))
		if err != nil {
			writeManagedResourceReadError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"resources": resources})
	}
}

func getManagedResource(application *automation.ManagedResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		resource, err := application.Get(
			request.Context(), identity, request.PathValue("projectID"),
			request.PathValue("kind"), request.PathValue("resourceID"),
		)
		if err != nil {
			writeManagedResourceReadError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"resource": resource})
	}
}

func readManagedResourceBackups(application *automation.ManagedResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		beforeMillis, ok := optionalNonNegativeInt64(response, request, "beforeMillis")
		if !ok {
			return
		}
		limit := 20
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				writeError(response, http.StatusBadRequest, "invalid_managed_resource_query", "limit must be 1..100")
				return
			}
			limit = parsed
		}
		status, err := application.BackupStatus(
			request.Context(), identity, request.PathValue("projectID"), request.PathValue("kind"),
			request.PathValue("resourceID"), beforeMillis, limit,
		)
		if err != nil {
			writeManagedResourceReadError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, status)
	}
}

func optionalNonNegativeInt64(response http.ResponseWriter, request *http.Request, name string) (int64, bool) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		writeError(response, http.StatusBadRequest, "invalid_managed_resource_query", name+" must be a non-negative integer")
		return 0, false
	}
	return parsed, true
}

func writeManagedResourceReadError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, automation.ErrManagedResourceInput):
		writeError(response, http.StatusBadRequest, "invalid_managed_resource_query", err.Error())
	case errors.Is(err, state.ErrProjectNotFound),
		errors.Is(err, state.ErrManagedPostgresNotFound),
		errors.Is(err, state.ErrManagedRedisNotFound),
		errors.Is(err, state.ErrObjectStoreNotFound):
		writeRepositoryError(response, err)
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to read managed resource metadata")
	}
}
