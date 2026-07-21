package server_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

type projectVerifier struct{}

func (projectVerifier) Verify(context.Context, string) (access.Identity, error) {
	return access.Identity{Subject: "subject", Email: "admin@example.com"}, nil
}

func TestProjectAPIRequiresAccessAndCommitsAudit(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw := server.Handler(server.DefaultMeta("ready"), server.WithProjects(store))
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	identity := projectRequest(http.MethodGet, "/api/v1/me", "")
	identityResponse := httptest.NewRecorder()
	protected.ServeHTTP(identityResponse, identity)
	if identityResponse.Code != http.StatusOK || !strings.Contains(identityResponse.Body.String(), `"email":"admin@example.com"`) {
		t.Fatalf("identity status/body = %d/%s", identityResponse.Code, identityResponse.Body)
	}

	create := projectRequest(http.MethodPost, "/api/v1/projects", "{\"name\":\"shop\"}")
	create.Header.Set("Origin", "https://admin.example.com")
	createResponse := httptest.NewRecorder()
	protected.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Location") == "" || createResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("create status/headers = %d, %v", createResponse.Code, createResponse.Header())
	}
	var created map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created["name"] != "shop" || created["id"] == "" {
		t.Fatalf("created project = %v", created)
	}

	list := projectRequest(http.MethodGet, "/api/v1/projects", "")
	listResponse := httptest.NewRecorder()
	protected.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listResponse.Code, listResponse.Body)
	}
	var projects []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0]["name"] != "shop" || projects[0]["serviceCount"] != float64(0) {
		t.Fatalf("projects = %v", projects)
	}

	canvas := projectRequest(http.MethodGet, "/api/v1/projects/"+created["id"].(string)+"/canvas", "")
	canvasResponse := httptest.NewRecorder()
	protected.ServeHTTP(canvasResponse, canvas)
	if canvasResponse.Code != http.StatusOK || !strings.Contains(canvasResponse.Body.String(), `"resources":[]`) || !strings.Contains(canvasResponse.Body.String(), `"connections":[]`) {
		t.Fatalf("canvas status/body = %d/%s", canvasResponse.Code, canvasResponse.Body)
	}
	missingCanvas := projectRequest(http.MethodGet, "/api/v1/projects/missing/canvas", "")
	missingCanvasResponse := httptest.NewRecorder()
	protected.ServeHTTP(missingCanvasResponse, missingCanvas)
	if missingCanvasResponse.Code != http.StatusNotFound || !strings.Contains(missingCanvasResponse.Body.String(), "project_not_found") {
		t.Fatalf("missing canvas status/body = %d/%s", missingCanvasResponse.Code, missingCanvasResponse.Body)
	}
	var auditCount int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE actor_id = 'subject' AND action = 'project.create'").Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit count = %d, %v", auditCount, err)
	}

	deleteWrongName := projectRequest(http.MethodDelete, "/api/v1/projects/"+created["id"].(string), `{"expectedName":"wrong","deleteBackups":false}`)
	deleteWrongName.Header.Set("Origin", "https://admin.example.com")
	deleteWrongNameResponse := httptest.NewRecorder()
	protected.ServeHTTP(deleteWrongNameResponse, deleteWrongName)
	if deleteWrongNameResponse.Code != http.StatusConflict {
		t.Fatalf("wrong-name deletion status/body = %d/%s", deleteWrongNameResponse.Code, deleteWrongNameResponse.Body)
	}

	deleteRequest := projectRequest(http.MethodDelete, "/api/v1/projects/"+created["id"].(string), `{"expectedName":"shop","deleteBackups":false}`)
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	protected.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || deleteResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("delete status/headers = %d, %v: %s", deleteResponse.Code, deleteResponse.Header(), deleteResponse.Body)
	}
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events WHERE actor_id = 'subject' AND action = 'project.delete'").Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("delete audit count = %d, %v", auditCount, err)
	}

	directResponse := httptest.NewRecorder()
	raw.ServeHTTP(directResponse, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if directResponse.Code != http.StatusForbidden {
		t.Fatalf("unprotected project API status = %d", directResponse.Code)
	}
	directIdentityResponse := httptest.NewRecorder()
	raw.ServeHTTP(directIdentityResponse, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if directIdentityResponse.Code != http.StatusForbidden {
		t.Fatalf("unprotected identity API status = %d", directIdentityResponse.Code)
	}
}

func TestProjectAPIRejectsInvalidAndDuplicateNames(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := access.ProtectAdmin(
		"admin.example.com",
		projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithProjects(store)),
	)
	for _, test := range []struct {
		body   string
		status int
		code   string
	}{
		{body: "{\"name\":\"Not valid\"}", status: http.StatusBadRequest, code: "invalid_name"},
		{body: "{\"name\":\"shop\",\"extra\":true}", status: http.StatusBadRequest, code: "invalid_json"},
		{body: "{\"name\":\"shop\"} {}", status: http.StatusBadRequest, code: "invalid_json"},
		{body: "{\"name\":\"shop\"}", status: http.StatusCreated},
		{body: "{\"name\":\"shop\"}", status: http.StatusConflict, code: "project_name_conflict"},
	} {
		request := projectRequest(http.MethodPost, "/api/v1/projects", test.body)
		request.Header.Set("Origin", "https://admin.example.com")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || (test.code != "" && !strings.Contains(response.Body.String(), "\""+test.code+"\"")) {
			t.Fatalf("body %q: status/body = %d/%s", test.body, response.Code, response.Body)
		}
	}
}

func projectRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "https://admin.example.com"+path, strings.NewReader(body))
	request.Host = "admin.example.com"
	request.TLS = &tls.ConnectionState{ServerName: "admin.example.com"}
	request.RemoteAddr = "203.0.113.5:43210"
	request.Header.Set("Cf-Access-Jwt-Assertion", "token")
	request.Header.Set("Accept", "application/json")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
