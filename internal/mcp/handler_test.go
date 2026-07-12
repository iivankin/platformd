package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type repositoryStub struct {
	projects      []state.ProjectSummary
	canvas        state.ProjectCanvas
	service       state.ServiceDesired
	deployments   state.DeploymentPage
	canvasCalls   int
	projectCalls  int
	projectsCalls int
}

func (repository *repositoryStub) Projects(context.Context) ([]state.ProjectSummary, error) {
	repository.projectsCalls++
	return repository.projects, nil
}

func (repository *repositoryStub) Project(_ context.Context, projectID string) (state.ProjectSummary, error) {
	repository.projectCalls++
	for _, project := range repository.projects {
		if project.ID == projectID {
			return project, nil
		}
	}
	return state.ProjectSummary{}, state.ErrProjectNotFound
}

func (repository *repositoryStub) ProjectCanvas(context.Context, string) (state.ProjectCanvas, error) {
	repository.canvasCalls++
	return repository.canvas, nil
}

func (repository *repositoryStub) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return repository.service, nil
}

func (repository *repositoryStub) ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error) {
	return repository.deployments, nil
}

func TestMCPStatelessLifecycleAndTransportContract(t *testing.T) {
	handler, err := New(Config{Hostname: "api.example.com", Version: "1.2.3", Repository: &repositoryStub{}})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://api.example.com/mcp", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET response = %d/%s", response.Code, response.Header().Get("Allow"))
	}

	initialize := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`)
	initialize.Header.Del("MCP-Protocol-Version")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, initialize)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-11-25"`) || !strings.Contains(response.Body.String(), `"tools":{}`) || strings.Contains(response.Body.String(), "listChanged") || response.Header().Get("MCP-Session-Id") != "" {
		t.Fatalf("initialize response = %d/%s headers=%v", response.Code, response.Body, response.Header())
	}

	initialized := mcpRequest(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	initialized.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, initialized)
	if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
		t.Fatalf("initialized notification = %d/%q", response.Code, response.Body.String())
	}

	list := mcpRequest(`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`)
	list.Header.Del("MCP-Protocol-Version")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing protocol header status = %d", response.Code)
	}
	list = mcpRequest(`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"list_projects"`) || !strings.Contains(response.Body.String(), `"name":"get_service"`) {
		t.Fatalf("tools/list response = %d/%s", response.Code, response.Body)
	}
}

func TestMCPRejectsInvalidTransportHeaders(t *testing.T) {
	handler, err := New(Config{Hostname: "api.example.com", Version: "1.2.3", Repository: &repositoryStub{}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`

	request := mcpRequest(body)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotAcceptable {
		t.Fatalf("single Accept status = %d", response.Code)
	}
	request = mcpRequest(body)
	request.Header.Set("Origin", "https://evil.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid Origin status = %d", response.Code)
	}
	request = mcpRequest(body)
	request = request.WithContext(context.Background())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token identity status = %d", response.Code)
	}
}

func TestMCPReadToolsEnforceProjectBoundaryBeforeLookup(t *testing.T) {
	boundProject := "project-a"
	repository := &repositoryStub{
		projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}, {ID: "project-b", Name: "beta"}},
		canvas: state.ProjectCanvas{Resources: []state.CanvasResource{{
			ID: "service", Kind: "service", Name: "api", Enabled: true, Status: "running",
			InternalHostname: "api.alpha.internal",
		}}},
	}
	handler, err := New(Config{Hostname: "api.example.com", Version: "1.2.3", Repository: repository})
	if err != nil {
		t.Fatal(err)
	}

	listProjects := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`)
	listProjects = withMCPIdentity(listProjects, automation.Identity{TokenID: "token", Role: "read", ProjectID: &boundProject})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, listProjects)
	if !strings.Contains(response.Body.String(), `\"id\":\"project-a\"`) || strings.Contains(response.Body.String(), `project-b`) {
		t.Fatalf("bounded list_projects = %s", response.Body)
	}
	if repository.projectCalls != 1 || repository.projectsCalls != 0 {
		t.Fatalf("bound project lookup used global list: exact=%d global=%d", repository.projectCalls, repository.projectsCalls)
	}

	otherProject := mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_services","arguments":{"projectId":"project-b"}}}`)
	otherProject = withMCPIdentity(otherProject, automation.Identity{TokenID: "token", Role: "read", ProjectID: &boundProject})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, otherProject)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.canvasCalls != 0 {
		t.Fatalf("cross-project tool result = %s, canvas calls=%d", response.Body, repository.canvasCalls)
	}
}

func mcpRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/mcp", strings.NewReader(body))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	return withMCPIdentity(request, automation.Identity{TokenID: "token", Role: "read"})
}

func withMCPIdentity(request *http.Request, identity automation.Identity) *http.Request {
	return request.WithContext(automation.WithIdentity(request.Context(), identity))
}
