package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

func TestServiceAPICommitsCanonicalDesiredConfigAndCanvasNode(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithProjects(store), server.WithServices(store)),
	)
	create := projectRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/services", `{
  "name":"api",
  "imageReference":"alpine:3.22",
  "environment":{"APP_ENV":"production"},
  "targetPort":8080,
  "healthPath":"/healthz",
  "enabled":false
}`)
	create.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"imageReference":"docker.io/library/alpine:3.22"`) || !strings.Contains(response.Body.String(), `"startupTimeoutSeconds":60`) {
		t.Fatalf("create status/body = %d/%s", response.Code, response.Body)
	}
	canvas := projectRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/canvas", "")
	canvasResponse := httptest.NewRecorder()
	handler.ServeHTTP(canvasResponse, canvas)
	if canvasResponse.Code != http.StatusOK || !strings.Contains(canvasResponse.Body.String(), `"kind":"service"`) || !strings.Contains(canvasResponse.Body.String(), `"internalHostname":"api.shop.internal"`) {
		t.Fatalf("canvas status/body = %d/%s", canvasResponse.Code, canvasResponse.Body)
	}
}

func TestServiceAPIRejectsCredentialForAnotherRegistry(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateImageRegistryCredential(context.Background(), state.CreateImageRegistryCredential{
		ImageRegistryCredential: state.ImageRegistryCredential{
			ID: "credential", ProjectID: "project", Name: "production",
			RegistryHost: "registry.example.com", Username: "robot",
			PasswordEncrypted: []byte("ciphertext"), CreatedAtMillis: 2,
		},
		AuditEventID: "credential-audit", ActorID: "actor", ActorEmail: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServices(store)),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services", `{
  "name":"api",
  "imageReference":"other.example.com/team/api:latest",
  "imageCredentialId":"credential"
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"image_credential_registry_mismatch"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
}
