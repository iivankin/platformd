package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerports"
	"github.com/iivankin/platformd/internal/server"
)

type containerPortResources struct{}

func (containerPortResources) Resource(_ context.Context, projectID, kind, resourceID string) error {
	if projectID != "project" || kind != "service" || resourceID != "api" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

type containerPortRuntime struct{}

func (containerPortRuntime) ResourceContainer(kind, resourceID string) (containerengine.Container, bool, error) {
	if kind != "service" || resourceID != "api" {
		return containerengine.Container{}, false, io.ErrUnexpectedEOF
	}
	return containerengine.Container{ID: "container", State: "running"}, true, nil
}

type containerPortEngine struct{}

func (containerPortEngine) ContainerListeningPorts(containerID string) ([]containerengine.ListeningPort, error) {
	if containerID != "container" {
		return nil, io.ErrUnexpectedEOF
	}
	return []containerengine.ListeningPort{{Port: 8080, Protocol: "tcp"}}, nil
}

func TestContainerPortsRequireAccessAndReturnLiveSockets(t *testing.T) {
	application, err := containerports.New(containerPortResources{}, containerPortRuntime{}, containerPortEngine{})
	if err != nil {
		t.Fatal(err)
	}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithContainerPorts(application))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/projects/project/resources/service/api/ports", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated ports = %d/%s", response.Code, response.Body)
	}

	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/projects/project/resources/service/api/ports", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"port":8080`) || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("ports response = %d/%s cache=%q", response.Code, response.Body, response.Header().Get("Cache-Control"))
	}
}
