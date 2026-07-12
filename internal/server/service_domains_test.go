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
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestServiceDomainAPIRequiresExplicitMoveAndDetaches(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	port := 8080
	for index, projectID := range []string{"project-a", "project-b"} {
		if _, err := store.CreateProject(context.Background(), state.CreateProject{
			ID: projectID, Name: projectID, AuditEventID: "project-audit-" + projectID,
			ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: int64(index + 1),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateService(context.Background(), state.CreateService{
			ID: "service-" + projectID, ProjectID: projectID, Name: "api", Enabled: true,
			Snapshot:     serviceconfig.Snapshot{ImageReference: "alpine", TargetPort: &port},
			AuditEventID: "service-audit-" + projectID, ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
			CreatedAtMillis: int64(index + 3),
		}); err != nil {
			t.Fatal(err)
		}
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithDomains(store)),
	)
	pathA := "/api/v1/projects/project-a/services/service-project-a/domains"
	attach := projectRequest(http.MethodPost, pathA, `{"hostname":"App.Example.com"}`)
	attach.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, attach)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"hostname":"app.example.com"`) {
		t.Fatalf("attach = %d/%s", response.Code, response.Body)
	}

	pathB := "/api/v1/projects/project-b/services/service-project-b/domains"
	conflict := projectRequest(http.MethodPost, pathB, `{"hostname":"app.example.com"}`)
	conflict.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, conflict)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"domain_conflict"`) || !strings.Contains(response.Body.String(), `"serviceId":"service-project-a"`) {
		t.Fatalf("conflict = %d/%s", response.Code, response.Body)
	}

	move := projectRequest(http.MethodPost, pathB, `{"hostname":"app.example.com","move":true}`)
	move.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, move)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"serviceId":"service-project-b"`) {
		t.Fatalf("move = %d/%s", response.Code, response.Body)
	}

	detach := projectRequest(http.MethodDelete, pathB+"/app.example.com", "")
	detach.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, detach)
	if response.Code != http.StatusNoContent {
		t.Fatalf("detach = %d/%s", response.Code, response.Body)
	}
}
