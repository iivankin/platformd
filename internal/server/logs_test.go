package server_test

import (
	"context"
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
	calls int
}

func (repository *logRepository) ServiceLogs(context.Context, string, string, string, string, int) (containerlogs.Window, error) {
	repository.calls++
	return containerlogs.Window{Records: []containerlogs.Record{{
		Timestamp: time.Unix(1, 0).UTC(), Stream: "stdout", Text: "ready",
		DeploymentID: "deployment", AttemptID: "attempt",
	}}}, nil
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
