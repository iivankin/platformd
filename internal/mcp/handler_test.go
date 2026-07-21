package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type repositoryStub struct {
	projects      []state.ProjectSummary
	canvas        state.ProjectCanvas
	service       state.ServiceDesired
	deployments   state.DeploymentPage
	canvasCalls   int
	projectCalls  int
	projectsCalls int
	serviceCalls  int
	createCalls   int
	created       state.CreateService
	volumes       []state.Volume
	volumeCreate  state.CreateVolume
}

func (repository *repositoryStub) CreateService(_ context.Context, input state.CreateService) (state.ServiceDesired, error) {
	repository.createCalls++
	repository.created = input
	return state.ServiceDesired{ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, Enabled: input.Enabled, Snapshot: input.Snapshot}, nil
}

func (*repositoryStub) UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) List(context.Context, managedimages.Engine, int, int, string) (managedimages.Page, error) {
	return managedimages.Page{Tags: []managedimages.Tag{{Name: "7.4-alpine"}}, Page: 1, PageSize: 50}, nil
}

func newTestHandler(t *testing.T, repository *repositoryStub) *Handler {
	t.Helper()
	services, err := automation.NewServiceApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := automation.NewLogApplication(repository, logReaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	volumeDomain, err := volume.New(volume.Config{
		Repository: repository, Filesystem: mcpVolumeFilesystem{},
	})
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := automation.NewVolumeApplication(volumeDomain)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{
		Hostname: "api.example.com", Version: "1.2.3", Repository: repository,
		Services: services, Logs: logs, Images: repository, Volumes: volumes, Admission: admission.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type mcpVolumeFilesystem struct{}

func (mcpVolumeFilesystem) Ensure(context.Context, state.PersistentVolumeReference) error { return nil }
func (mcpVolumeFilesystem) Remove(context.Context, string, string) error                  { return nil }

func TestMCPListsOfficialManagedImageTagsForReadToken(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	call := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_managed_image_tags","arguments":{"engine":"redis","search":"alpine"}}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `7.4-alpine`) {
		t.Fatalf("managed image tool = %s", response.Body)
	}
}

type logReaderStub struct{}

func (logReaderStub) Read(context.Context, containerlogs.Query) (containerlogs.Window, error) {
	return containerlogs.Window{Records: []containerlogs.Record{{Text: "ready"}}}, nil
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
	repository.serviceCalls++
	return repository.service, nil
}

func (repository *repositoryStub) CreateVolume(_ context.Context, input state.CreateVolume) (state.Volume, error) {
	repository.volumeCreate = input
	repository.volumes = append(repository.volumes, input.Volume)
	return input.Volume, nil
}

func (repository *repositoryStub) VolumesByService(context.Context, string, string) ([]state.Volume, error) {
	return repository.volumes, nil
}

func (repository *repositoryStub) DeleteVolume(_ context.Context, input state.DeleteVolume) (state.Volume, error) {
	for index, item := range repository.volumes {
		if item.ID == input.VolumeID {
			repository.volumes = append(repository.volumes[:index], repository.volumes[index+1:]...)
			return item, nil
		}
	}
	return state.Volume{}, state.ErrVolumeNotFound
}

func (repository *repositoryStub) ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error) {
	return repository.deployments, nil
}

func TestMCPStatelessLifecycleAndTransportContract(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})

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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"list_projects"`) || !strings.Contains(response.Body.String(), `"name":"get_service"`) || !strings.Contains(response.Body.String(), `"name":"read_service_logs"`) || !strings.Contains(response.Body.String(), `"name":"list_service_volumes"`) || strings.Contains(response.Body.String(), `"name":"create_service_volume"`) {
		t.Fatalf("tools/list response = %d/%s", response.Code, response.Body)
	}
}

func TestMCPVolumeToolsUseReadAndAdminBoundaries(t *testing.T) {
	repository := &repositoryStub{}
	handler := newTestHandler(t, repository)

	create := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_service_volume","arguments":{"projectId":"project","serviceId":"service","name":"data"}}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if strings.Contains(createResponse.Body.String(), `"isError":true`) || len(repository.volumes) != 1 || repository.volumeCreate.ActorID != "admin" {
		t.Fatalf("create volume = %s state=%+v", createResponse.Body, repository.volumeCreate)
	}

	list := mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_service_volumes","arguments":{"projectId":"project","serviceId":"service"}}}`)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if strings.Contains(listResponse.Body.String(), `"isError":true`) || !strings.Contains(listResponse.Body.String(), `\"name\":\"data\"`) {
		t.Fatalf("list volumes = %s", listResponse.Body)
	}

	deleteRequest := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_service_volume","arguments":{"projectId":"project","serviceId":"service","volumeId":"`+repository.volumes[0].ID+`"}}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if strings.Contains(deleteResponse.Body.String(), `"isError":true`) || len(repository.volumes) != 0 {
		t.Fatalf("delete volume = %s", deleteResponse.Body)
	}
}

func TestMCPReadServiceLogsEnforcesBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{service: state.ServiceDesired{ID: "service", ProjectID: "project-a"}}
	handler := newTestHandler(t, repository)
	bound := "project-a"

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_service_logs","arguments":{"projectId":"project-b","serviceId":"service"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.serviceCalls != 0 {
		t.Fatalf("cross-project logs = %s calls=%d", response.Body, repository.serviceCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_service_logs","arguments":{"projectId":"project-a","serviceId":"service","limit":10}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `ready`) || repository.serviceCalls != 1 {
		t.Fatalf("visible logs = %s calls=%d", response.Body, repository.serviceCalls)
	}
}

func TestMCPRejectsInvalidTransportHeaders(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
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
	handler := newTestHandler(t, repository)

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

func TestMCPAdminToolVisibilityAndAuthorizationBeforeMutation(t *testing.T) {
	repository := &repositoryStub{}
	handler := newTestHandler(t, repository)

	list := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if strings.Contains(response.Body.String(), `"name":"create_service"`) {
		t.Fatalf("read tools exposed admin mutation: %s", response.Body)
	}

	list = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"create_service"`) || !strings.Contains(response.Body.String(), `"name":"rollback_service"`) {
		t.Fatalf("admin tools missing mutations: %s", response.Body)
	}

	call := mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_service","arguments":"not-an-object"}}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `admin token is required`) || repository.createCalls != 0 {
		t.Fatalf("read mutation = %s, calls=%d", response.Body, repository.createCalls)
	}

	boundProject := "project-a"
	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_service","arguments":{"projectId":"project-b","name":"api","configuration":{"source":{"type":"public_image","autoUpdate":true,"image":{"reference":"alpine"}}}}}}`), automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &boundProject})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.createCalls != 0 {
		t.Fatalf("bound admin mutation = %s, calls=%d", response.Body, repository.createCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"create_service","arguments":{"projectId":"project-a","name":"api","configuration":{"source":{"type":"public_image","autoUpdate":true,"image":{"reference":"alpine"}}}}}}`), automation.Identity{TokenID: "admin-token", Role: "admin", ProjectID: &boundProject})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `requestId`) || repository.createCalls != 1 {
		t.Fatalf("admin mutation = %s, calls=%d", response.Body, repository.createCalls)
	}
	if repository.created.ActorKind != "token" || repository.created.ActorID != "admin-token" || repository.created.ActorEmail != "" {
		t.Fatalf("mutation actor = %+v", repository.created)
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
