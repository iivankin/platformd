package mcp

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/rootexec"
	"github.com/iivankin/platformd/internal/state"
)

type mcpServerExecRunner struct{ calls int }

func (runner *mcpServerExecRunner) Execute(context.Context, rootexec.Request) (rootexec.Result, error) {
	runner.calls++
	return rootexec.Result{
		Stdout: "uid=0(root)\n", ExitCode: 0,
		StartedAt: 10, FinishedAt: 20, DurationMillis: 10,
	}, nil
}

type mcpServerExecAudit struct{ calls int }

func (audit *mcpServerExecAudit) RecordServerExec(context.Context, state.RecordServerExec) error {
	audit.calls++
	return nil
}

func TestMCPServerExecIsVisibleAndCallableOnlyByUnboundAdmin(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	runner := &mcpServerExecRunner{}
	audit := &mcpServerExecAudit{}
	application, err := automation.NewServerExecApplication(
		runner, audit, bytes.NewReader(make([]byte, 64)),
		func() time.Time { return time.UnixMilli(10) },
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.serverExec = application

	list := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if strings.Contains(response.Body.String(), `"name":"server_exec"`) {
		t.Fatalf("read token saw root tool: %s", response.Body.String())
	}

	projectID := "project"
	list = withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`),
		automation.Identity{TokenID: "bound", Role: "admin", ProjectID: &projectID},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if strings.Contains(response.Body.String(), `"name":"server_exec"`) {
		t.Fatalf("project-bound admin saw root tool: %s", response.Body.String())
	}

	list = withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`),
		automation.Identity{TokenID: "root", Role: "admin"},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"server_exec"`) {
		t.Fatalf("unbound admin root tool list = %s", response.Body.String())
	}

	call := withMCPIdentity(
		mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"server_exec","arguments":{"command":"id","timeoutSeconds":12}}}`),
		automation.Identity{TokenID: "root", Role: "admin"},
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) ||
		!strings.Contains(response.Body.String(), `uid=0(root)`) ||
		!strings.Contains(response.Body.String(), `requestId`) || runner.calls != 1 || audit.calls != 1 {
		t.Fatalf("server exec result = %s, exec=%d audit=%d", response.Body.String(), runner.calls, audit.calls)
	}
}
