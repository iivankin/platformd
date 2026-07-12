package automationapi

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
	projectCalls  int
	projectsCalls int
	canvasCalls   int
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

func (*repositoryStub) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, state.ErrServiceNotFound
}

func (*repositoryStub) ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error) {
	return state.DeploymentPage{}, nil
}

func TestAutomationAPIEnforcesProjectBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{projects: []state.ProjectSummary{
		{ID: "project-a", Name: "alpha"}, {ID: "project-b", Name: "beta"},
	}}
	handler, err := Handler(Config{Hostname: "api.example.com", Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
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
	handler, err := Handler(Config{Hostname: "api.example.com", Repository: &repositoryStub{}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/openapi.json", automation.Identity{TokenID: "token", Role: "read"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"openapi":"3.1.0"`) || !strings.Contains(response.Body.String(), `"url":"https://api.example.com"`) {
		t.Fatalf("OpenAPI response = %d/%s", response.Code, response.Body)
	}
}

func automationRequest(path string, identity automation.Identity) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.com"+path, nil)
	return request.WithContext(automation.WithIdentity(request.Context(), identity))
}
