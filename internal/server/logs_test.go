package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/server"
)

type logRepository struct {
	calls         int
	resourceCalls int
	resourceKind  string
	downloadCalls int
	downloadQuery containerlogs.DownloadQuery
}

func (repository *logRepository) DownloadServiceLogs(_ context.Context, _ string, query containerlogs.DownloadQuery, destination io.Writer) (containerlogs.DownloadResult, error) {
	repository.downloadCalls++
	repository.downloadQuery = query
	const payload = "{\"type\":\"platformd.log_export_complete\",\"records\":0,\"truncated\":false}\n"
	written, err := io.WriteString(destination, payload)
	return containerlogs.DownloadResult{Bytes: int64(written)}, err
}

func (repository *logRepository) ServiceLogs(context.Context, string, string, string, string, int) (containerlogs.Window, error) {
	repository.calls++
	return containerlogs.Window{Records: []containerlogs.Record{{
		Timestamp: time.Unix(1, 0).UTC(), Stream: "stdout", Text: "ready",
		DeploymentID: "deployment", AttemptID: "attempt",
	}}}, nil
}

func (repository *logRepository) ResourceLogs(_ context.Context, _, kind, _, _, _ string, _ int) (containerlogs.Window, error) {
	repository.resourceCalls++
	repository.resourceKind = kind
	return containerlogs.Window{Records: []containerlogs.Record{{
		Timestamp: time.Unix(1, 0).UTC(), Stream: "stdout", Text: "resource ready",
		DeploymentID: "resource", AttemptID: "attempt",
	}}}, nil
}

func TestAdminServiceLogDownloadRequiresAccessAndBoundsRange(t *testing.T) {
	repository := &logRepository{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithLogs("admin.example.com", repository))
	path := "/api/v1/projects/project/services/service/logs/download?from=1783843200000&to=1783846800000&deploymentId=deployment"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden || repository.downloadCalls != 0 {
		t.Fatalf("unauthenticated download = %d/%s calls=%d", response.Code, response.Body, repository.downloadCalls)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || repository.downloadCalls != 1 || repository.downloadQuery.DeploymentID != "deployment" ||
		response.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" ||
		!strings.Contains(response.Header().Get("Content-Disposition"), "platformd-service-logs.ndjson") ||
		!strings.Contains(response.Body.String(), "log_export_complete") {
		t.Fatalf("download = %d/%s headers=%v query=%+v", response.Code, response.Body, response.Header(), repository.downloadQuery)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/logs/download?from=1783843200000&to=1783933200001", ""))
	if response.Code != http.StatusBadRequest || repository.downloadCalls != 1 {
		t.Fatalf("overlong download = %d/%s calls=%d", response.Code, response.Body, repository.downloadCalls)
	}
}

func (*logRepository) ServiceLogRevision(context.Context, string, string, string, string) (string, error) {
	return "revision", nil
}

func TestAdminServiceLogsRequireAccessAndReturnStructuredWindow(t *testing.T) {
	repository := &logRepository{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithLogs("admin.example.com", repository))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/projects/project/services/service/logs", nil))
	if response.Code != http.StatusForbidden || repository.calls != 0 {
		t.Fatalf("unauthenticated logs = %d/%s calls=%d", response.Code, response.Body, repository.calls)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/logs?limit=20&contains=ready", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stream":"stdout"`) || !strings.Contains(response.Body.String(), `"text":"ready"`) || repository.calls != 1 {
		t.Fatalf("authenticated logs = %d/%s calls=%d", response.Code, response.Body, repository.calls)
	}
}

func TestAdminManagedResourceLogsRequireAccessAndUseScopedKind(t *testing.T) {
	repository := &logRepository{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithLogs("admin.example.com", repository))
	path := "/api/v1/projects/project/postgres/database/logs?limit=20&contains=ready"
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden || repository.resourceCalls != 0 {
		t.Fatalf("unauthenticated resource logs = %d/%s calls=%d", response.Code, response.Body, repository.resourceCalls)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, path, ""))
	if response.Code != http.StatusOK || repository.resourceCalls != 1 || repository.resourceKind != "postgres" ||
		!strings.Contains(response.Body.String(), `"text":"resource ready"`) {
		t.Fatalf("resource logs = %d/%s calls=%d kind=%q", response.Code, response.Body, repository.resourceCalls, repository.resourceKind)
	}
}
