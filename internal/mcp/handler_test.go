package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/resourcemetrics"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
	"github.com/iivankin/platformd/internal/volume"
)

type repositoryStub struct {
	projects        []state.ProjectSummary
	canvas          state.ProjectCanvas
	service         state.ServiceDesired
	deployments     state.DeploymentPage
	canvasCalls     int
	projectCalls    int
	projectsCalls   int
	serviceCalls    int
	createCalls     int
	created         state.CreateService
	volumes         []state.Volume
	volumeCreate    state.CreateVolume
	telemetryCalls  int
	telemetryMethod string
	telemetryPath   string
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

func (*repositoryStub) DeleteService(context.Context, state.DeleteServiceInput) (state.DeleteServiceResult, error) {
	return state.DeleteServiceResult{}, nil
}

func (*repositoryStub) RestartServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) RemoveServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) List(context.Context, managedimages.Engine, int, int, string) (managedimages.Page, error) {
	return managedimages.Page{Tags: []managedimages.Tag{{Name: "7.4-alpine"}}, Page: 1, PageSize: 50}, nil
}

func newTestHandler(t *testing.T, repository *repositoryStub) *Handler {
	t.Helper()
	services, err := automation.NewServiceApplication(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := automation.NewLogApplication(repository, logReaderStub{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := automation.NewUsageApplication(repository, usageMetricsStub{})
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
		Hostname: "admin.example.com", Version: "1.2.3", Repository: repository,
		Services: services, Logs: logs, Usage: usage, Images: repository, Volumes: volumes,
		Telemetry: repository, Admission: admission.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func (repository *repositoryStub) QueryService(_ context.Context, serviceID, method, path string, _ any) (any, error) {
	repository.telemetryCalls++
	repository.telemetryMethod = method
	repository.telemetryPath = path
	return map[string]any{"serviceId": serviceID, "path": path}, nil
}

func (*repositoryStub) ServiceTelemetry(_ context.Context, _, serviceID string) (telemetry.ServiceConfiguration, error) {
	return telemetry.ServiceConfiguration{
		ServiceID: serviceID, InternalHostname: "sentry-api.project.internal",
		InternalDSN:          "http://service@sentry-api.project.internal:9001/1",
		InternalOTLPEndpoint: "http://otel-api.project.internal:4318",
		PublicHostname:       "errors.example.com", PublicDSN: "https://service@errors.example.com/1", UpdatedAt: 10,
	}, nil
}

func (*repositoryStub) RotateServiceArtifactToken(context.Context, string, string) (string, error) {
	return "artifact-secret", nil
}

func (*repositoryStub) ServiceTelemetryWebhooks(context.Context, string, string) ([]state.ServiceTelemetryWebhook, error) {
	return []state.ServiceTelemetryWebhook{{
		ID: "webhook", URL: "https://hooks.example.com/telemetry", EventTypes: []string{"issue_created"},
		SecretEncrypted: []byte("must-not-leak"), Enabled: true, CreatedAtMillis: 1, UpdatedAtMillis: 1,
	}}, nil
}

func (*repositoryStub) CreateServiceTelemetryWebhook(_ context.Context, _, serviceID, value string, events []string) (state.ServiceTelemetryWebhook, string, error) {
	return state.ServiceTelemetryWebhook{ID: "created-webhook", ServiceID: serviceID, URL: value, EventTypes: events, Enabled: true, CreatedAtMillis: 2, UpdatedAtMillis: 2}, "webhook-secret", nil
}

func (*repositoryStub) DeleteServiceTelemetryWebhook(context.Context, string, string, string) error {
	return nil
}

func (*repositoryStub) MetricCharts(_ context.Context, scope state.MetricScope) ([]state.MetricChart, error) {
	return []state.MetricChart{{ID: "chart", Scope: scope, Title: "Requests", SQL: "SELECT 1", Visualization: "line", Legend: "requests", CreatedAtMillis: 1, UpdatedAtMillis: 1}}, nil
}

func (*repositoryStub) CreateMetricChart(_ context.Context, chart state.MetricChart) (state.MetricChart, error) {
	chart.ID, chart.CreatedAtMillis, chart.UpdatedAtMillis = "created-chart", 2, 2
	return chart, nil
}

func (*repositoryStub) UpdateMetricChart(_ context.Context, chart state.MetricChart, expected int64) (state.MetricChart, error) {
	chart.CreatedAtMillis, chart.UpdatedAtMillis = 1, expected+1
	return chart, nil
}

func (*repositoryStub) DeleteMetricChart(context.Context, state.MetricScope, string) error {
	return nil
}

func (*repositoryStub) QueryMetricScope(_ context.Context, _ state.MetricScope, operation string, _ json.RawMessage) (telemetry.MetricScopeResponse, error) {
	body := []byte(`[{"name":"http.server.duration"}]`)
	if operation == "query" {
		body = []byte(`[{"timeUnixNano":"1","value":2}]`)
	}
	return telemetry.MetricScopeResponse{Body: body, ContentType: "application/json", StatusCode: http.StatusOK}, nil
}

func (*repositoryStub) UpdateServiceTelemetryPublicAccess(_ context.Context, input state.UpdateServiceSentryPublicAccess) (telemetry.ServiceConfiguration, error) {
	return telemetry.ServiceConfiguration{ServiceID: input.ID, PublicHostname: input.PublicHostname, PublicDSN: "https://service@" + input.PublicHostname + "/1", UpdatedAt: input.UpdatedAtMillis}, nil
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

type usageMetricsStub struct{}

func (usageMetricsStub) Read(cgroupstats.Kind, string) (resourcemetrics.Current, error) {
	cpu := int64(100)
	return resourcemetrics.Current{
		Sample: cgroupstats.Sample{ObservedAtMillis: 1, MemoryBytes: 2048, Running: true}, CPUMillicores: &cpu,
	}, nil
}

func (usageMetricsStub) History(context.Context, cgroupstats.Kind, string, time.Duration) (resourcemetrics.History, error) {
	return resourcemetrics.History{
		From: 1, To: 2, StepMillis: 60000,
		Points: []resourcemetrics.Point{{ObservedAt: 1, MemoryBytes: 1024, Running: true}},
	}, nil
}

func (usageMetricsStub) ReadProject(string) (resourcemetrics.Current, error) {
	return resourcemetrics.Current{
		Sample:           cgroupstats.Sample{ObservedAtMillis: 2, MemoryBytes: 4096, Running: true},
		RunningResources: 1, TotalResources: 1,
	}, nil
}

func (usageMetricsStub) ProjectHistory(context.Context, string, time.Duration) (resourcemetrics.History, error) {
	return resourcemetrics.History{
		From: 3, To: 4, StepMillis: 300000,
		Series: []resourcemetrics.HistorySeries{{
			ID: "service", Kind: "service", Name: "api",
			Points: []resourcemetrics.Point{{ObservedAt: 3, MemoryBytes: 512, Running: true}},
		}},
	}, nil
}

func (usageMetricsStub) ReadInstallation() (resourcemetrics.Current, error) {
	return resourcemetrics.Current{
		Sample: cgroupstats.Sample{ObservedAtMillis: 5, MemoryBytes: 8192, Running: true},
	}, nil
}

func (usageMetricsStub) InstallationHistory(context.Context, time.Duration) (resourcemetrics.History, error) {
	return resourcemetrics.History{From: 5, To: 6, StepMillis: 60000}, nil
}

func (usageMetricsStub) HostHistory(context.Context, time.Duration) (resourcemetrics.History, error) {
	return resourcemetrics.History{From: 7, To: 8, StepMillis: 60000}, nil
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

func (repository *repositoryStub) ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error) {
	return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
}

func (repository *repositoryStub) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	return state.ManagedRedis{}, state.ErrManagedRedisNotFound
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://admin.example.com/public/mcp", nil))
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"list_projects"`) || !strings.Contains(response.Body.String(), `"name":"get_service"`) || !strings.Contains(response.Body.String(), `"name":"read_service_logs"`) || !strings.Contains(response.Body.String(), `"name":"read_resource_usage"`) || !strings.Contains(response.Body.String(), `"name":"read_project_usage"`) || !strings.Contains(response.Body.String(), `"name":"list_service_volumes"`) || strings.Contains(response.Body.String(), `"name":"create_service_volume"`) {
		t.Fatalf("tools/list response = %d/%s", response.Code, response.Body)
	}
}

func TestMCPAdvertisesStandaloneAgentGuidanceAndSafetyHints(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})

	initialize := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`)
	initialize.Header.Del("MCP-Protocol-Version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initialize)
	var initialized struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initialized); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"list_projects", "expectedUpdatedAt", "list_service_issues", "get_metric_catalog", "Internal artifact uploads need no token"} {
		if !strings.Contains(initialized.Result.Instructions, expected) {
			t.Fatalf("initialize instructions omit %q: %q", expected, initialized.Result.Instructions)
		}
	}

	list := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), automation.Identity{TokenID: "admin", Role: "admin"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	var listed struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Tool, len(listed.Result.Tools))
	for _, tool := range listed.Result.Tools {
		byName[tool.Name] = tool
	}
	if tool := byName["list_service_issues"]; tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint {
		t.Fatalf("issue tool annotations = %+v", tool.Annotations)
	}
	if tool := byName["rotate_service_artifact_token"]; tool.Annotations == nil || tool.Annotations.ReadOnlyHint || !tool.Annotations.DestructiveHint {
		t.Fatalf("credential rotation annotations = %+v", tool.Annotations)
	}
	metric := byName["query_metrics"]
	properties, _ := metric.InputSchema["properties"].(map[string]any)
	step, _ := properties["step"].(map[string]any)
	if step["minimum"] != float64(1_000) || !strings.Contains(metric.Description, "virtual metrics table") {
		t.Fatalf("metric agent contract = %#v / %q", step, metric.Description)
	}
}

func TestMCPSupportsCodexProtocolVersion(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})

	initialize := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"codex","version":"0.146.0-alpha.9.2"}}}`)
	initialize.Header.Set("MCP-Protocol-Version", protocolVersion2025June18)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initialize)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("Codex initialize response = %d/%s", response.Code, response.Body)
	}

	initialized := mcpRequest(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	initialized.Header.Set("MCP-Protocol-Version", protocolVersion2025June18)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, initialized)
	if response.Code != http.StatusAccepted {
		t.Fatalf("Codex initialized notification = %d/%s", response.Code, response.Body)
	}

	list := mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	list.Header.Set("MCP-Protocol-Version", protocolVersion2025June18)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"list_projects"`) {
		t.Fatalf("Codex tools/list response = %d/%s", response.Code, response.Body)
	}
}

func TestMCPInitializeNegotiatesLatestSupportedProtocolVersion(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	initialize := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01","capabilities":{},"clientInfo":{"name":"future-client","version":"1"}}}`)
	initialize.Header.Del("MCP-Protocol-Version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initialize)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("negotiated initialize response = %d/%s", response.Code, response.Body)
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

func TestMCPServiceTelemetryUsesCommonToolsAndTokenBoundary(t *testing.T) {
	repository := &repositoryStub{service: state.ServiceDesired{ID: "service", ProjectID: "project-a"}}
	handler := newTestHandler(t, repository)
	bound := "project-a"
	read := automation.Identity{TokenID: "read", Role: "read", ProjectID: &bound}

	list := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), read)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if !strings.Contains(response.Body.String(), `"name":"list_service_issues"`) || strings.Contains(response.Body.String(), `"name":"update_service_issue_status"`) {
		t.Fatalf("read telemetry tools = %s", response.Body)
	}

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_service_issues","arguments":{"projectId":"project-b","serviceId":"service"}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.serviceCalls != 0 || repository.telemetryCalls != 0 {
		t.Fatalf("cross-project telemetry tool = %s calls=%d/%d", response.Body, repository.serviceCalls, repository.telemetryCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_service_issues","arguments":{"projectId":"project-a","serviceId":"service","limit":10}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || repository.telemetryCalls != 1 || repository.telemetryMethod != http.MethodGet || repository.telemetryPath != "/issues?limit=10" {
		t.Fatalf("read telemetry tool = %s forwarded=%s %s", response.Body, repository.telemetryMethod, repository.telemetryPath)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"update_service_issue_status","arguments":{"projectId":"project-a","serviceId":"service","issueId":"issue","status":"resolved"}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), "admin token is required") || repository.telemetryCalls != 1 {
		t.Fatalf("read telemetry mutation = %s calls=%d", response.Body, repository.telemetryCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"update_service_issue_status","arguments":{"projectId":"project-a","serviceId":"service","issueId":"issue","status":"resolved"}}}`), automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || repository.telemetryCalls != 2 || repository.telemetryMethod != http.MethodPatch || repository.telemetryPath != "/issues/issue" {
		t.Fatalf("admin telemetry mutation = %s forwarded=%s %s", response.Body, repository.telemetryMethod, repository.telemetryPath)
	}
}

func TestMCPServiceTelemetryCoversConfigurationTracesMetricsAndMutations(t *testing.T) {
	repository := &repositoryStub{
		service:  state.ServiceDesired{ID: "service", ProjectID: "project-a"},
		projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}},
	}
	handler := newTestHandler(t, repository)
	bound := "project-a"
	read := automation.Identity{TokenID: "read", Role: "read", ProjectID: &bound}
	admin := automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}

	list := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), read)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	for _, name := range []string{"get_service_telemetry", "get_service_replay_recording", "list_service_traces", "get_service_trace", "get_metric_catalog", "query_metrics", "list_metric_charts"} {
		if !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("read telemetry tools omit %s: %s", name, response.Body)
		}
	}
	if strings.Contains(response.Body.String(), `"name":"rotate_service_artifact_token"`) {
		t.Fatalf("read token sees telemetry mutation: %s", response.Body)
	}

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_service_telemetry","arguments":{"projectId":"project-a","serviceId":"service"}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `internalOtlpEndpoint`) || strings.Contains(response.Body.String(), `must-not-leak`) {
		t.Fatalf("service telemetry config = %s", response.Body)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_service_traces","arguments":{"projectId":"project-a","serviceId":"service","query":"invoice 42","from":1000,"to":2000,"limit":200,"offset":4}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || repository.telemetryPath != "/traces?from=1000&limit=200&offset=4&query=invoice+42&to=2000" {
		t.Fatalf("trace list = %s path=%s", response.Body, repository.telemetryPath)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_service_replay_recording","arguments":{"projectId":"project-a","serviceId":"service","replayId":"replay","offset":10}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || repository.telemetryPath != "/replays/replay/recording?limit=500&offset=10" {
		t.Fatalf("replay recording = %s path=%s", response.Body, repository.telemetryPath)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"query_metrics","arguments":{"scope":"project","projectId":"project-a","sql":"SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket","from":1000,"to":2000,"step":1000}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `timeUnixNano`) {
		t.Fatalf("metric SQL = %s", response.Body)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rotate_service_artifact_token","arguments":{"projectId":"project-a","serviceId":"service"}}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `artifact-secret`) || !strings.Contains(response.Body.String(), `SENTRY_AUTH_TOKEN`) {
		t.Fatalf("artifact credential = %s", response.Body)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_service_telemetry_webhook","arguments":{"projectId":"project-a","serviceId":"service","url":"https://hooks.example.com/new","events":["issue_created"]}}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `webhook-secret`) ||
		!strings.Contains(response.Body.String(), `X-Platformd-Signature`) || !strings.Contains(response.Body.String(), `raw request body`) {
		t.Fatalf("create webhook = %s", response.Body)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"create_metric_chart","arguments":{"scope":"service","projectId":"project-a","serviceId":"service","title":"Latency","sql":"SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket","visualization":"line","legend":"latency"}}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `created-chart`) {
		t.Fatalf("create metric chart = %s", response.Body)
	}
}

func TestMCPGetProjectEnforcesBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}}}
	handler := newTestHandler(t, repository)
	bound := "project-a"

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_project","arguments":{"projectId":"project-b"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.projectCalls != 0 {
		t.Fatalf("cross-project get_project = %s calls=%d", response.Body, repository.projectCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_project","arguments":{"projectId":"project-a"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `\"name\":\"alpha\"`) || repository.projectCalls != 1 {
		t.Fatalf("visible get_project = %s calls=%d", response.Body, repository.projectCalls)
	}
}

func TestMCPListAuditEventsEnforcesBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}}}
	handler := newTestHandler(t, repository)
	audit := &auditRepositoryStub{}
	application, err := automation.NewAuditApplication(audit)
	if err != nil {
		t.Fatal(err)
	}
	handler.audit = application
	handler.tools = append(handler.tools, listAuditEventsTool())
	bound := "project-a"

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_audit_events","arguments":{"projectId":"project-b"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || audit.calls != 0 {
		t.Fatalf("cross-project audit = %s calls=%d", response.Body, audit.calls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_audit_events","arguments":{"projectId":"project-a"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || audit.calls != 1 || audit.query.ProjectID != "project-a" {
		t.Fatalf("visible audit = %s calls=%d query=%+v", response.Body, audit.calls, audit.query)
	}
}

func TestMCPDeleteServiceEnforcesBoundaryBeforeLookup(t *testing.T) {
	repository := &deleteServiceRepositoryStub{}
	services, err := automation.NewServiceApplication(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, &repositoryStub{})
	handler.services = services
	bound := "project-a"

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_service","arguments":{"projectId":"project-b","serviceId":"service","expectedUpdatedAt":1}}}`), automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.deleteCalls != 0 {
		t.Fatalf("cross-project delete = %s calls=%d", response.Body, repository.deleteCalls)
	}
}

type auditRepositoryStub struct {
	calls int
	query state.AuditQuery
}

func (repository *auditRepositoryStub) AuditEvents(_ context.Context, query state.AuditQuery) (state.AuditPage, error) {
	repository.calls++
	repository.query = query
	return state.AuditPage{}, nil
}

type deleteServiceRepositoryStub struct {
	deleteCalls int
}

func (*deleteServiceRepositoryStub) CreateService(context.Context, state.CreateService) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}
func (*deleteServiceRepositoryStub) UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}
func (*deleteServiceRepositoryStub) RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}
func (*deleteServiceRepositoryStub) RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}
func (repository *deleteServiceRepositoryStub) DeleteService(context.Context, state.DeleteServiceInput) (state.DeleteServiceResult, error) {
	repository.deleteCalls++
	return state.DeleteServiceResult{}, nil
}
func (*deleteServiceRepositoryStub) RestartServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}
func (*deleteServiceRepositoryStub) RemoveServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func TestMCPReadUsageEnforcesBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{
		projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}},
		service:  state.ServiceDesired{ID: "service", ProjectID: "project-a"},
	}
	handler := newTestHandler(t, repository)
	bound := "project-a"

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_resource_usage","arguments":{"projectId":"project-b","kind":"service","resourceId":"service"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), `"isError":true`) || repository.serviceCalls != 0 {
		t.Fatalf("cross-project usage = %s calls=%d", response.Body, repository.serviceCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_resource_usage","arguments":{"projectId":"project-a","kind":"service","resourceId":"service"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `\"memoryBytes\":2048`) || repository.serviceCalls != 1 {
		t.Fatalf("visible usage = %s calls=%d", response.Body, repository.serviceCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_project_usage","arguments":{"projectId":"project-a","range":"1h"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `\"series\"`) || repository.projectCalls != 1 {
		t.Fatalf("project usage history = %s projectCalls=%d", response.Body, repository.projectCalls)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_resource_usage","arguments":{"projectId":"project-a","kind":"bucket","resourceId":"assets"}}}`), automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":-32602`) {
		t.Fatalf("invalid kind = %s", response.Body)
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

	request = mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	request.Header.Set("MCP-Protocol-Version", "2099-01-01")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsupported protocol header status = %d", response.Code)
	}
}

func TestMCPToolsCallAndListAcceptProtocolMeta(t *testing.T) {
	repository := &repositoryStub{
		projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}},
	}
	handler := newTestHandler(t, repository)

	list := mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"progressToken":"list-1"}}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"error"`) || !strings.Contains(response.Body.String(), `"name":"list_projects"`) {
		t.Fatalf("tools/list with _meta = %s", response.Body)
	}

	call := mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_projects","arguments":{},"_meta":{"progressToken":1}}}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"isError":true`) || strings.Contains(response.Body.String(), `Invalid tools/call params`) || !strings.Contains(response.Body.String(), `\"id\":\"project-a\"`) {
		t.Fatalf("tools/call with _meta = %s", response.Body)
	}

	call = mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_projects","arguments":{"unexpected":true},"_meta":{}}}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":-32602`) || !strings.Contains(response.Body.String(), `list_projects requires an empty object`) {
		t.Fatalf("tools/call still rejects unknown tool arguments = %s", response.Body)
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
	request := httptest.NewRequest(http.MethodPost, "https://admin.example.com/public/mcp", strings.NewReader(body))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	return withMCPIdentity(request, automation.Identity{TokenID: "token", Role: "read"})
}

func withMCPIdentity(request *http.Request, identity automation.Identity) *http.Request {
	return request.WithContext(automation.WithIdentity(request.Context(), identity))
}
