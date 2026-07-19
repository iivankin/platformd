package server_test

import (
	"context"
	"errors"
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

type rejectingServiceImageCredentials struct{}

func (rejectingServiceImageCredentials) PrepareServiceImageCredential(context.Context, server.ServiceImageCredentialInput) (*state.ServiceImageCredential, error) {
	return nil, errors.New("credential host does not match image host")
}

func (rejectingServiceImageCredentials) RevealServiceImageCredential(context.Context, string) (string, string, string, error) {
	return "", "", "", errors.New("not available")
}

type visibleServiceImageCredentials struct{}

func (visibleServiceImageCredentials) PrepareServiceImageCredential(_ context.Context, input server.ServiceImageCredentialInput) (*state.ServiceImageCredential, error) {
	return &state.ServiceImageCredential{
		ServiceID: input.ServiceID, RegistryHost: "registry.example.com", Username: input.Username,
		PasswordEncrypted: []byte("encrypted"), UpdatedAtMillis: input.UpdatedAtMillis,
	}, nil
}

func (visibleServiceImageCredentials) RevealServiceImageCredential(context.Context, string) (string, string, string, error) {
	return "registry.example.com", "robot", "secret", nil
}

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
  "source":{"type":"public_image","autoUpdate":true,"image":{"reference":"alpine:3.22"}},
  "environment":{"APP_ENV":"production"},
  "healthCheck":{"port":8080,"path":"/healthz","timeoutSeconds":60},
  "enabled":false
}`)
	create.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"source":{"type":"public_image","autoUpdate":true,"image":{"reference":"docker.io/library/alpine:3.22"}}`) || !strings.Contains(response.Body.String(), `"healthCheck":{"port":8080,"path":"/healthz","timeoutSeconds":60}`) {
		t.Fatalf("create status/body = %d/%s", response.Code, response.Body)
	}
	canvas := projectRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/canvas", "")
	canvasResponse := httptest.NewRecorder()
	handler.ServeHTTP(canvasResponse, canvas)
	if canvasResponse.Code != http.StatusOK || !strings.Contains(canvasResponse.Body.String(), `"kind":"service"`) || !strings.Contains(canvasResponse.Body.String(), `"internalHostname":"api.shop.internal"`) || !strings.Contains(canvasResponse.Body.String(), `"status":"disabled"`) {
		t.Fatalf("canvas status/body = %d/%s", canvasResponse.Code, canvasResponse.Body)
	}
}

func TestServiceAPIRejectsInvalidInlineRegistryCredential(t *testing.T) {
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
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServices(store), server.WithServiceImageCredentials(rejectingServiceImageCredentials{})),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services", `{
  "name":"api",
  "source":{"type":"private_image","autoUpdate":true,"image":{"reference":"other.example.com/team/api:latest"}},
  "registryCredential":{"username":"robot","password":"secret"}
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_registry_auth"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
}

func TestServiceAPIReturnsOwnedRegistryPasswordWithoutCaching(t *testing.T) {
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
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServices(store), server.WithServiceImageCredentials(visibleServiceImageCredentials{})),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services", `{
  "name":"api",
  "enabled":false,
  "source":{"type":"private_image","autoUpdate":true,"image":{"reference":"registry.example.com/team/api:latest"}},
  "registryCredential":{"username":"robot","password":"secret"}
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || !strings.Contains(response.Body.String(), `"registryCredential":{"registryHost":"registry.example.com","username":"robot","password":"secret"}`) {
		t.Fatalf("create status/cache/body = %d/%q/%s", response.Code, response.Header().Get("Cache-Control"), response.Body)
	}

	serviceID := strings.TrimPrefix(response.Header().Get("Location"), "/api/v1/projects/project/services/")
	get := projectRequest(http.MethodGet, "/api/v1/projects/project/services/"+serviceID, "")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Header().Get("Cache-Control"), "no-store") || !strings.Contains(getResponse.Body.String(), `"password":"secret"`) {
		t.Fatalf("get status/cache/body = %d/%q/%s", getResponse.Code, getResponse.Header().Get("Cache-Control"), getResponse.Body)
	}
}
