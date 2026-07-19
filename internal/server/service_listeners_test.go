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

func TestServiceListenerAPIManagesExplicitPublicAndTargetPorts(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(ctx, state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine"),}, AuditEventID: "service-audit",
		ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServiceListeners(store)),
	)
	path := "/api/v1/projects/project/services/service/listeners"
	attach := projectRequest(http.MethodPost, path, `{"protocol":"udp","publicPort":53000,"targetPort":5300}`)
	attach.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, attach)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"publicPort":53000`) || !strings.Contains(response.Body.String(), `"targetPort":5300`) {
		t.Fatalf("attach = %d/%s", response.Code, response.Body)
	}
	list := projectRequest(http.MethodGet, path, "")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocol":"udp"`) {
		t.Fatalf("list = %d/%s", response.Code, response.Body)
	}
	reserved := projectRequest(http.MethodPost, path, `{"protocol":"tcp","publicPort":443,"targetPort":8443}`)
	reserved.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, reserved)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"public_port_reserved"`) {
		t.Fatalf("reserved = %d/%s", response.Code, response.Body)
	}
	detach := projectRequest(http.MethodDelete, path+"/udp/53000", "")
	detach.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, detach)
	if response.Code != http.StatusNoContent {
		t.Fatalf("detach = %d/%s", response.Code, response.Body)
	}
}
