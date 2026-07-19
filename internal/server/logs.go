package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type LogRepository interface {
	BuildLog(context.Context, string, string, string) (string, error)
	ServiceLogs(context.Context, string, string, string, string, int) (containerlogs.Window, error)
	ResourceLogs(context.Context, string, string, string, string, string, int) (containerlogs.Window, error)
	ServiceLogRevision(context.Context, string, string, string, string) (string, error)
	DownloadServiceLogs(context.Context, string, containerlogs.DownloadQuery, io.Writer) (containerlogs.DownloadResult, error)
}

const logStreamPollInterval = 250 * time.Millisecond

func registerLogRoutes(mux *http.ServeMux, hostname string, repository LogRepository) error {
	if hostname == "" {
		return errors.New("log stream hostname is required")
	}
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs", getServiceLogs(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/deployments/{deploymentID}/logs/build", getBuildLog(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{resourceID}/logs", getResourceLogs(repository, "redis"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{resourceID}/logs", getResourceLogs(repository, "postgres"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{resourceID}/logs", getResourceLogs(repository, "object_store"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs/download", downloadServiceLogs(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs/stream", streamServiceLogs(hostname, repository))
	return nil
}

func getBuildLog(repository LogRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		content, err := repository.BuildLog(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"), request.PathValue("deploymentID"),
		)
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, map[string]string{"text": content})
		case errors.Is(err, state.ErrServiceNotFound), errors.Is(err, state.ErrDeploymentNotFound):
			writeAPIError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found")
		default:
			writeAPIError(response, http.StatusInternalServerError, "build_log_read_failed", "Unable to read build log")
		}
	}
}

func getResourceLogs(repository LogRepository, kind string) http.HandlerFunc {
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
		window, err := repository.ResourceLogs(
			request.Context(), request.PathValue("projectID"), kind, request.PathValue("resourceID"),
			request.URL.Query().Get("deploymentId"), request.URL.Query().Get("contains"), limit,
		)
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, window)
		case errors.Is(err, state.ErrManagedRedisNotFound), errors.Is(err, state.ErrManagedPostgresNotFound), errors.Is(err, state.ErrObjectStoreNotFound):
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
		writeAPIError(response, http.StatusInternalServerError, "log_download_failed", "Unable to download service logs")
	}
}

func getServiceLogs(repository LogRepository) http.HandlerFunc {
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

type logStreamMessage struct {
	Type      string                 `json:"type"`
	Records   []containerlogs.Record `json:"records"`
	Truncated bool                   `json:"truncated,omitempty"`
}

func logStreamRecords(records []containerlogs.Record) []containerlogs.Record {
	if records == nil {
		return []containerlogs.Record{}
	}
	return records
}

func streamServiceLogs(hostname string, repository LogRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") != "https://"+hostname {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		limit, err := logLimit(request)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		if limit == 0 {
			limit = containerlogs.DefaultLimit
		}
		projectID := request.PathValue("projectID")
		serviceID := request.PathValue("serviceID")
		deploymentID := request.URL.Query().Get("deploymentId")
		contains := request.URL.Query().Get("contains")
		window, err := repository.ServiceLogs(request.Context(), projectID, serviceID, deploymentID, contains, limit)
		if err != nil {
			writeLogStreamError(response, err)
			return
		}
		revision, err := repository.ServiceLogRevision(request.Context(), projectID, serviceID, deploymentID, contains)
		if err != nil {
			writeLogStreamError(response, err)
			return
		}
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx := connection.CloseRead(context.Background())
		if err := writeLogMessage(ctx, connection, logStreamMessage{
			Type: "snapshot", Records: logStreamRecords(window.Records), Truncated: window.Truncated,
		}); err != nil {
			return
		}
		seen := logFingerprints(window.Records)
		poll := time.NewTicker(logStreamPollInterval)
		defer poll.Stop()
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := connection.Ping(pingCtx)
				cancel()
				if err != nil {
					return
				}
			case <-poll.C:
				currentRevision, revisionErr := repository.ServiceLogRevision(ctx, projectID, serviceID, deploymentID, contains)
				if revisionErr != nil {
					_ = connection.Close(websocket.StatusInternalError, "log stream unavailable")
					return
				}
				if currentRevision == revision {
					continue
				}
				current, readErr := repository.ServiceLogs(ctx, projectID, serviceID, deploymentID, contains, containerlogs.MaximumLimit)
				if readErr != nil {
					_ = connection.Close(websocket.StatusInternalError, "log stream unavailable")
					return
				}
				currentSeen := logFingerprints(current.Records)
				newRecords, overlap := unseenLogRecords(current.Records, seen)
				if len(seen) > 0 && !overlap && len(current.Records) > 0 {
					if err := writeLogMessage(ctx, connection, logStreamMessage{Type: "gap", Records: []containerlogs.Record{}}); err != nil {
						return
					}
				}
				if len(newRecords) > 0 {
					if err := writeLogMessage(ctx, connection, logStreamMessage{Type: "records", Records: newRecords}); err != nil {
						return
					}
				}
				seen = currentSeen
				revision = currentRevision
			}
		}
	}
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

func writeLogStreamError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrServiceNotFound):
		http.Error(response, "Service not found", http.StatusNotFound)
	case errors.Is(err, containerlogs.ErrInvalidQuery):
		http.Error(response, err.Error(), http.StatusBadRequest)
	default:
		http.Error(response, "Unable to read service logs", http.StatusInternalServerError)
	}
}

func writeLogMessage(ctx context.Context, connection *websocket.Conn, message logStreamMessage) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, connection, message)
}

type logFingerprint [sha256.Size]byte

func logFingerprints(records []containerlogs.Record) map[logFingerprint]struct{} {
	result := make(map[logFingerprint]struct{}, len(records))
	for _, record := range records {
		result[fingerprintLogRecord(record)] = struct{}{}
	}
	return result
}

func unseenLogRecords(records []containerlogs.Record, previous map[logFingerprint]struct{}) ([]containerlogs.Record, bool) {
	result := make([]containerlogs.Record, 0)
	overlap := false
	for _, record := range records {
		if _, exists := previous[fingerprintLogRecord(record)]; exists {
			overlap = true
			continue
		}
		result = append(result, record)
	}
	return result, overlap
}

func fingerprintLogRecord(record containerlogs.Record) logFingerprint {
	hash := sha256.New()
	_, _ = io.WriteString(hash, record.Timestamp.UTC().Format(time.RFC3339Nano))
	for _, value := range []string{record.Stream, record.Text, record.DeploymentID, record.AttemptID} {
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, value)
	}
	if record.Partial {
		_, _ = hash.Write([]byte{1})
	}
	if record.Truncated {
		_, _ = hash.Write([]byte{2})
	}
	var result logFingerprint
	copy(result[:], hash.Sum(nil))
	return result
}
