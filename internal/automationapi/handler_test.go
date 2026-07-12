package automationapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/state"
)

type repositoryStub struct {
	projects      []state.ProjectSummary
	projectCalls  int
	projectsCalls int
	canvasCalls   int
	createCalls   int
	created       state.CreateService
	serviceCalls  int
	service       state.ServiceDesired
}

func (repository *repositoryStub) CreateService(_ context.Context, input state.CreateService) (state.ServiceDesired, error) {
	repository.createCalls++
	repository.created = input
	return state.ServiceDesired{
		ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, Enabled: input.Enabled,
		Snapshot: input.Snapshot, CreatedAtMillis: input.CreatedAtMillis, UpdatedAtMillis: input.CreatedAtMillis,
	}, nil
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

func (*repositoryStub) List(context.Context, managedimages.Engine, int, int) (managedimages.Page, error) {
	return managedimages.Page{Tags: []managedimages.Tag{{Name: "18.3"}}, Page: 1, PageSize: 50}, nil
}

func automationHandler(t *testing.T, repository *repositoryStub) http.Handler {
	t.Helper()
	services, err := automation.NewServiceApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := automation.NewLogApplication(repository, logReaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(Config{
		Hostname: "api.example.com", Repository: repository, Services: services, Logs: logs, Images: repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAutomationAPIListsOfficialManagedImageTags(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/managed-images/postgres/tags?page=1&pageSize=50&search=18", automation.Identity{TokenID: "token", Role: "read"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"18.3"`) {
		t.Fatalf("managed image tags = %d/%s", response.Code, response.Body)
	}
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
	return state.ProjectCanvas{}, nil
}

func (repository *repositoryStub) Service(_ context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	repository.serviceCalls++
	if repository.service.ProjectID == projectID && repository.service.ID == serviceID {
		return repository.service, nil
	}
	return state.ServiceDesired{}, state.ErrServiceNotFound
}

type logReaderStub struct{}

func (logReaderStub) Read(context.Context, containerlogs.Query) (containerlogs.Window, error) {
	return containerlogs.Window{Records: []containerlogs.Record{{Text: "ready"}}}, nil
}

func (*repositoryStub) ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error) {
	return state.DeploymentPage{}, nil
}

func TestAutomationAPIEnforcesProjectBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{projects: []state.ProjectSummary{
		{ID: "project-a", Name: "alpha"}, {ID: "project-b", Name: "beta"},
	}}
	handler := automationHandler(t, repository)
	projectID := "project-a"
	identity := automation.Identity{TokenID: "token", Role: "read", ProjectID: &projectID}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects", identity))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"project-a"`) || strings.Contains(response.Body.String(), "project-b") {
		t.Fatalf("bounded project list = %d/%s", response.Code, response.Body)
	}
	if repository.projectCalls != 1 || repository.projectsCalls != 0 {
		t.Fatalf("bounded list used global lookup: exact=%d global=%d", repository.projectCalls, repository.projectsCalls)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-b/services", identity))
	if response.Code != http.StatusForbidden || repository.canvasCalls != 0 {
		t.Fatalf("cross-project request = %d/%s, lookups=%d", response.Code, response.Body, repository.canvasCalls)
	}
}

func TestAutomationAPIPublishesOpenAPIAndRequiresIdentity(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/openapi.json", automation.Identity{TokenID: "token", Role: "read"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"openapi":"3.1.0"`) || !strings.Contains(response.Body.String(), `"url":"https://api.example.com"`) || strings.Contains(response.Body.String(), `/query`) {
		t.Fatalf("OpenAPI response = %d/%s", response.Code, response.Body)
	}
}

func TestAutomationAPIReadsLogsWithinTokenProjectBoundary(t *testing.T) {
	repository := &repositoryStub{service: state.ServiceDesired{ID: "service", ProjectID: "project-a"}}
	handler := automationHandler(t, repository)
	bound := "project-a"
	identity := automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-b/services/service/logs", identity))
	if response.Code != http.StatusForbidden || repository.serviceCalls != 0 {
		t.Fatalf("cross-project logs = %d/%s calls=%d", response.Code, response.Body, repository.serviceCalls)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-a/services/service/logs?limit=10", identity))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"text":"ready"`) || repository.serviceCalls != 1 {
		t.Fatalf("visible logs = %d/%s calls=%d", response.Code, response.Body, repository.serviceCalls)
	}
}

func TestAutomationAPIRequiresAdminBeforeDecodingAndCreatesTokenActor(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects/project/services"

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-json"))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || repository.createCalls != 0 {
		t.Fatalf("read mutation = %d/%s, calls=%d", response.Code, response.Body, repository.createCalls)
	}

	request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{
  "name":"api",
  "configuration":{"imageReference":"alpine:3.22"}
}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "admin-token", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.createCalls != 1 {
		t.Fatalf("admin mutation = %d/%s, calls=%d", response.Code, response.Body, repository.createCalls)
	}
	if repository.created.ActorKind != "token" || repository.created.ActorID != "admin-token" || repository.created.ActorEmail != "" {
		t.Fatalf("mutation actor = %+v", repository.created)
	}
}

func automationRequest(path string, identity automation.Identity) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.com"+path, nil)
	return request.WithContext(automation.WithIdentity(request.Context(), identity))
}
