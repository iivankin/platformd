package automationapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
)

type registryApplicationStub struct {
	listCalls       int
	createInput     registry.CreateRepositoryInput
	credentialInput registry.CreateCredentialInput
}

func (application *registryApplicationStub) RepositorySummaries(context.Context) ([]registry.RepositorySummary, error) {
	application.listCalls++
	return []registry.RepositorySummary{{
		Repository:    state.RegistryRepository{ID: "repository", Name: "apps", PublicPull: true},
		ManifestCount: 2, TagCount: 1, BlobCount: 3, TotalBlobBytes: 42,
	}}, nil
}

func (*registryApplicationStub) RepositorySummary(context.Context, string) (registry.RepositorySummary, error) {
	return registry.RepositorySummary{Repository: state.RegistryRepository{ID: "repository", Name: "apps"}}, nil
}

func (application *registryApplicationStub) CreateRepository(_ context.Context, input registry.CreateRepositoryInput) (registry.CreateRepositoryResult, error) {
	application.createInput = input
	return registry.CreateRepositoryResult{
		Repository: state.RegistryRepository{ID: "repository", Name: input.Name, PublicPull: input.PublicPull},
		Credential: state.RegistryCredential{ID: "credential", Name: "default", Permission: "pull_push"},
		Username:   "registry-user", Secret: "one-time-secret", RequestID: "create-request",
	}, nil
}

func (*registryApplicationStub) SetPublicPull(_ context.Context, input registry.SetPublicPullInput) (state.RegistryRepository, string, error) {
	return state.RegistryRepository{ID: input.RepositoryID, Name: "apps", PublicPull: input.PublicPull}, "policy-request", nil
}

func (*registryApplicationStub) Images(context.Context, string, string, int) ([]registry.Image, bool, error) {
	return []registry.Image{{Digest: "sha256:abc", Tags: []string{"latest"}}}, false, nil
}

func (*registryApplicationStub) Image(context.Context, string, string) (registry.Image, error) {
	return registry.Image{Digest: "sha256:abc", ManifestJSON: []byte(`{"schemaVersion":2}`)}, nil
}

func (*registryApplicationStub) DeleteTag(context.Context, registry.DeleteInput) (string, string, error) {
	return "sha256:abc", "tag-request", nil
}

func (*registryApplicationStub) DeleteManifest(context.Context, registry.DeleteInput) ([]string, string, error) {
	return []string{"latest"}, "manifest-request", nil
}

func (*registryApplicationStub) DeleteRepository(context.Context, registry.DeleteInput) (string, error) {
	return "delete-request", nil
}

func (*registryApplicationStub) Credentials(context.Context, string) ([]state.RegistryCredential, error) {
	return []state.RegistryCredential{{
		ID: "credential", RepositoryID: "repository", Name: "robot", Permission: "pull",
		SecretHMAC: []byte("must-not-leak"),
	}}, nil
}

func (application *registryApplicationStub) CreateCredential(_ context.Context, input registry.CreateCredentialInput) (registry.CreateCredentialResult, error) {
	application.credentialInput = input
	return registry.CreateCredentialResult{
		Credential: state.RegistryCredential{ID: "credential", Name: input.Name, Permission: input.Permission},
		Username:   "registry-user", Secret: "credential-secret", RequestID: "credential-request",
	}, nil
}

func (*registryApplicationStub) DeleteCredential(context.Context, string, string, registry.Actor) (string, error) {
	return "credential-delete-request", nil
}

func (*registryApplicationStub) Cleanup(context.Context, string, bool, registry.Actor) (registry.CleanupResult, error) {
	return registry.CleanupResult{BlobCount: 1, Bytes: 42, PreviewDigests: []string{"sha256:orphan"}}, nil
}

type registrySettingsStub struct {
	getCalls int
	input    state.SetRegistryHostnameInput
}

func (settings *registrySettingsStub) RegistryHostname(context.Context) (string, error) {
	settings.getCalls++
	return "registry.example.com", nil
}

func (settings *registrySettingsStub) SetRegistryHostname(_ context.Context, input state.SetRegistryHostnameInput) (*string, error) {
	settings.input = input
	return &input.Hostname, nil
}

func registryHandlerForTest(t *testing.T, application registryApplication, settings registrySettings) http.Handler {
	t.Helper()
	repository := &repositoryStub{}
	projects, err := automation.NewProjectApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	services, err := automation.NewServiceApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := automation.NewLogApplication(repository, logReaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(Config{
		Hostname: "api.example.com", Repository: repository, Projects: projects,
		Services: services, Logs: logs, Images: repository, Registry: application,
		RegistrySettings: settings, Admission: admission.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAutomationRegistryRejectsProjectBoundTokensBeforeLookup(t *testing.T) {
	application := &registryApplicationStub{}
	settings := &registrySettingsStub{}
	handler := registryHandlerForTest(t, application, settings)
	projectID := "project"
	identity := automation.Identity{TokenID: "bound", Role: "read", ProjectID: &projectID}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/registry/repositories", identity))
	if response.Code != http.StatusForbidden || application.listCalls != 0 {
		t.Fatalf("bound repository list = %d/%s calls=%d", response.Code, response.Body, application.listCalls)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/registry", identity))
	if response.Code != http.StatusForbidden || settings.getCalls != 0 {
		t.Fatalf("bound settings read = %d/%s calls=%d", response.Code, response.Body, settings.getCalls)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/registry/repositories", automation.Identity{TokenID: "read", Role: "read"}))
	if response.Code != http.StatusOK || application.listCalls != 1 || !strings.Contains(response.Body.String(), `"name":"apps"`) {
		t.Fatalf("unbound repository list = %d/%s calls=%d", response.Code, response.Body, application.listCalls)
	}

	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/registry/repositories", strings.NewReader("not-json"))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || application.createInput.Actor.ID != "" {
		t.Fatalf("read-token mutation = %d/%s input=%+v", response.Code, response.Body, application.createInput)
	}
}

func TestAutomationRegistryUsesTokenActorsAndReturnsSecretsOnlyAtCreation(t *testing.T) {
	application := &registryApplicationStub{}
	settings := &registrySettingsStub{}
	handler := registryHandlerForTest(t, application, settings)

	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/registry/repositories", strings.NewReader(`{"name":"apps","publicPull":true}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "root-token", Role: "admin"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || application.createInput.Actor != (registry.Actor{Kind: "token", ID: "root-token"}) ||
		!strings.Contains(response.Body.String(), `"secret":"one-time-secret"`) {
		t.Fatalf("repository create = %d/%s input=%+v", response.Code, response.Body, application.createInput)
	}
	request = httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/registry/repositories/repository/credentials", strings.NewReader(`{"name":"robot","permission":"pull"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "root-token", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || application.credentialInput.Actor != (registry.Actor{Kind: "token", ID: "root-token"}) ||
		!strings.Contains(response.Body.String(), `"secret":"credential-secret"`) {
		t.Fatalf("credential create = %d/%s input=%+v", response.Code, response.Body, application.credentialInput)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/registry/repositories/repository/credentials", automation.Identity{TokenID: "read", Role: "read"}))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "must-not-leak") || strings.Contains(response.Body.String(), `"secret"`) {
		t.Fatalf("credential list = %d/%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPut, "https://api.example.com/api/v1/registry/hostname", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "root-token", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || settings.input.ActorID != "" {
		t.Fatalf("missing hostname = %d/%s input=%+v", response.Code, response.Body, settings.input)
	}

	request = httptest.NewRequest(http.MethodPut, "https://api.example.com/api/v1/registry/hostname", strings.NewReader(`{"hostname":"registry.example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "root-token", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || settings.input.ActorKind != "token" || settings.input.ActorID != "root-token" ||
		settings.input.AuditEventID == "" || settings.input.RequestCorrelationID == "" {
		t.Fatalf("hostname update = %d/%s input=%+v", response.Code, response.Body, settings.input)
	}
}

func TestAutomationRegistryOpenAPIDescribesAdminRoutesWithoutDockerProtocol(t *testing.T) {
	handler := registryHandlerForTest(t, &registryApplicationStub{}, &registrySettingsStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/openapi.json", automation.Identity{TokenID: "read", Role: "read"}))
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d/%s", response.Code, response.Body)
	}
	var document struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Paths["/api/v1/registry/repositories"]["post"] == nil ||
		document.Paths["/api/v1/registry/repositories/{repositoryID}/cleanup"]["post"] == nil ||
		document.Paths["/api/v1/registry/hostname"]["put"] == nil {
		t.Fatalf("Registry OpenAPI paths are incomplete: %s", response.Body)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/v2/", automation.Identity{TokenID: "read", Role: "read"}))
	if response.Code != http.StatusNotFound {
		t.Fatalf("Docker Registry protocol exposed on automation handler: %d/%s", response.Code, response.Body)
	}
}
