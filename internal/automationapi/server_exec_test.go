package automationapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/rootexec"
	"github.com/iivankin/platformd/internal/state"
)

type serverExecRunnerStub struct {
	input rootexec.Request
	calls int
}

func (runner *serverExecRunnerStub) Execute(_ context.Context, input rootexec.Request) (rootexec.Result, error) {
	runner.calls++
	runner.input = input
	return rootexec.Result{
		Stdout: "root\n", Stderr: "warning\n", ExitCode: 7,
		StartedAt: 10, FinishedAt: 20, DurationMillis: 10,
	}, nil
}

type serverExecAuditStub struct{ calls int }

func (audit *serverExecAuditStub) RecordServerExec(context.Context, state.RecordServerExec) error {
	audit.calls++
	return nil
}

func TestServerExecRESTRequiresUnboundAdminAndReturnsBoundedResult(t *testing.T) {
	runner := &serverExecRunnerStub{}
	audit := &serverExecAuditStub{}
	application, err := automation.NewServerExecApplication(
		runner, audit, bytes.NewReader(make([]byte, 64)),
		func() time.Time { return time.UnixMilli(10) },
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := executeServerCommand(application)
	request := httptest.NewRequest(
		http.MethodPost, "https://api.example.com/api/v1/server/exec",
		strings.NewReader(`{"command":"id","timeoutSeconds":12}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{
		TokenID: "admin", Role: "admin",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stdout":"root\n"`) ||
		!strings.Contains(response.Body.String(), `"stderr":"warning\n"`) ||
		!strings.Contains(response.Body.String(), `"exitCode":7`) || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("server exec response = %d/%s/%s", response.Code, response.Header(), response.Body.String())
	}
	if runner.calls != 1 || runner.input.Command != "id" || runner.input.Timeout != 12*time.Second || audit.calls != 1 {
		t.Fatalf("server exec dependencies = calls=%d input=%+v audit=%d", runner.calls, runner.input, audit.calls)
	}

	projectID := "project"
	request = httptest.NewRequest(
		http.MethodPost, "https://api.example.com/api/v1/server/exec",
		strings.NewReader(`{"command":"id"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{
		TokenID: "bound", Role: "admin", ProjectID: &projectID,
	}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"unbound_admin_required"`) || runner.calls != 1 {
		t.Fatalf("bound token response = %d/%s calls=%d", response.Code, response.Body.String(), runner.calls)
	}
}

func TestServerExecOpenAPIPathMatchesConfiguredRoute(t *testing.T) {
	response := httptest.NewRecorder()
	serveOpenAPI("api.example.com", false).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if strings.Contains(response.Body.String(), `"/api/v1/server/exec"`) {
		t.Fatalf("disabled server exec was advertised: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	serveOpenAPI("api.example.com", true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if !strings.Contains(response.Body.String(), `"/api/v1/server/exec"`) ||
		!strings.Contains(response.Body.String(), `"ServerExecRequest"`) {
		t.Fatalf("enabled server exec is absent from OpenAPI: %s", response.Body.String())
	}
}
