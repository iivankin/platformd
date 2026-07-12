package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type imageCredentialRepository struct {
	created server.CreateImageCredential
}

func (repository *imageCredentialRepository) ImageCredentials(context.Context, string) ([]state.ImageRegistryCredential, error) {
	return []state.ImageRegistryCredential{{
		ID: "existing", ProjectID: "project", Name: "existing", RegistryHost: "registry.example.com",
		Username: "robot", CreatedAtMillis: 1,
	}}, nil
}

func (repository *imageCredentialRepository) CreateImageCredential(_ context.Context, input server.CreateImageCredential) (state.ImageRegistryCredential, error) {
	repository.created = input
	return state.ImageRegistryCredential{
		ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, RegistryHost: input.RegistryHost,
		Username: input.Username, PasswordEncrypted: []byte("must-not-leak"), CreatedAtMillis: input.CreatedAtMillis,
	}, nil
}

func TestImageCredentialAPIValidatesAndNeverReturnsPasswordMaterial(t *testing.T) {
	repository := &imageCredentialRepository{}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithImageCredentials(repository))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	create := projectRequest(http.MethodPost, "/api/v1/projects/project/image-credentials", `{
  "name":"production",
  "registryHost":"REGISTRY.EXAMPLE.COM:5443",
  "username":"robot",
  "password":"super-secret"
}`)
	create.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated || repository.created.RegistryHost != "registry.example.com:5443" || repository.created.Password != "super-secret" {
		t.Fatalf("status/input = %d/%+v: %s", response.Code, repository.created, response.Body)
	}
	if strings.Contains(response.Body.String(), "super-secret") || strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("credential secret leaked in response: %s", response.Body)
	}

	list := projectRequest(http.MethodGet, "/api/v1/projects/project/image-credentials", "")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"registryHost":"registry.example.com"`) {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body)
	}
}
