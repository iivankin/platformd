package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type LogRepository interface {
	ResourceLogs(context.Context, string, containerlogs.ResourceQuery) (containerlogs.Window, error)
	DownloadServiceLogs(context.Context, string, containerlogs.DownloadQuery, io.Writer) (containerlogs.DownloadResult, error)
}

func registerLogRoutes(mux *http.ServeMux, repository LogRepository) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs", getResourceLogs(repository, "service", "serviceID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{resourceID}/logs", getResourceLogs(repository, "redis", "resourceID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{resourceID}/logs", getResourceLogs(repository, "postgres", "resourceID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs/download", downloadServiceLogs(repository))
}

func getResourceLogs(repository LogRepository, kind, resourcePathValue string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		limit, err := logLimit(request)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_log_limit", err.Error())
			return
		}
		from, to, err := optionalLogRange(request)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_log_range", err.Error())
			return
		}
		fieldFilters, err := logFieldFilters(request)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_log_field_filters", err.Error())
			return
		}
		window, err := repository.ResourceLogs(
			request.Context(), request.PathValue("projectID"), containerlogs.ResourceQuery{
				Kind: kind, ResourceID: request.PathValue(resourcePathValue),
				Query: containerlogs.Query{
					DeploymentID: request.URL.Query().Get("deploymentId"), Contains: request.URL.Query().Get("contains"),
					Cursor: request.URL.Query().Get("cursor"), FieldFilters: fieldFilters,
					From: from, To: to, Limit: limit,
				},
			},
		)
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, window)
		case errors.Is(err, state.ErrServiceNotFound), errors.Is(err, state.ErrManagedRedisNotFound), errors.Is(err, state.ErrManagedPostgresNotFound):
			writeAPIError(response, http.StatusNotFound, "resource_not_found", "Resource not found")
		case errors.Is(err, containerlogs.ErrInvalidQuery):
			writeAPIError(response, http.StatusBadRequest, "invalid_log_query", err.Error())
		default:
			writeAPIError(response, http.StatusInternalServerError, "log_read_failed", "Unable to read resource logs")
		}
	}
}

func downloadServiceLogs(repository LogRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		from, to, err := logDownloadRange(request)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_log_range", err.Error())
			return
		}
		serviceID := request.PathValue("serviceID")
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Disposition", `attachment; filename="platformd-service-logs.ndjson"`)
		response.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		result, err := repository.DownloadServiceLogs(request.Context(), request.PathValue("projectID"), containerlogs.DownloadQuery{
			ServiceID: serviceID, DeploymentID: request.URL.Query().Get("deploymentId"), From: from, To: to,
		}, response)
		if err == nil || result.Bytes != 0 {
			return
		}
		response.Header().Del("Content-Disposition")
		writeLogDownloadError(response, err)
	}
}

func logDownloadRange(request *http.Request) (time.Time, time.Time, error) {
	fromMillis, err := strconv.ParseInt(request.URL.Query().Get("from"), 10, 64)
	if err != nil || fromMillis <= 0 {
		return time.Time{}, time.Time{}, errors.New("from must be a positive Unix millisecond timestamp")
	}
	toMillis, err := strconv.ParseInt(request.URL.Query().Get("to"), 10, 64)
	if err != nil || toMillis <= 0 {
		return time.Time{}, time.Time{}, errors.New("to must be a positive Unix millisecond timestamp")
	}
	from := time.UnixMilli(fromMillis)
	to := time.UnixMilli(toMillis)
	if !to.After(from) || to.Sub(from) > containerlogs.MaximumDownloadRange {
		return time.Time{}, time.Time{}, errors.New("download range must be greater than zero and at most 24 hours")
	}
	return from, to, nil
}

func writeLogDownloadError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, containerlogs.ErrInvalidQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_log_query", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "log_download_failed", "Unable to download service logs", err)
	}
}

func logFieldFilters(request *http.Request) ([]containerlogs.FieldFilter, error) {
	value := request.URL.Query().Get("fieldFilters")
	if value == "" {
		return nil, nil
	}
	if len(value) > containerlogs.MaximumFieldFilterBytes {
		return nil, errors.New("fieldFilters is too large")
	}
	var filters []containerlogs.FieldFilter
	if err := json.Unmarshal([]byte(value), &filters); err != nil {
		return nil, errors.New("fieldFilters must be a JSON array")
	}
	return filters, nil
}

func optionalLogRange(request *http.Request) (time.Time, time.Time, error) {
	parse := func(name string) (time.Time, error) {
		value := request.URL.Query().Get(name)
		if value == "" {
			return time.Time{}, nil
		}
		millis, err := strconv.ParseInt(value, 10, 64)
		if err != nil || millis <= 0 {
			return time.Time{}, fmt.Errorf("%s must be a positive Unix millisecond timestamp", name)
		}
		return time.UnixMilli(millis), nil
	}
	from, err := parse("from")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parse("to")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return time.Time{}, time.Time{}, errors.New("to must not be earlier than from")
	}
	return from, to, nil
}

func logLimit(request *http.Request) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > containerlogs.MaximumLimit {
		return 0, fmt.Errorf("limit must be an integer from 1 to %d", containerlogs.MaximumLimit)
	}
	return parsed, nil
}
