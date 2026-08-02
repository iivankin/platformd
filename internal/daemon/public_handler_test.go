package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/server"
)

func TestPublicHandlerExposesOnlyExactPublicEndpoints(t *testing.T) {
	marker := func(value string) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
	}
	handler := publicHandler(
		marker("github"),
		marker("forward"),
		marker("protected"),
	)

	for _, test := range []struct {
		method string
		path   string
		want   string
	}{
		{method: http.MethodPost, path: server.GitHubWebhookPath, want: "github"},
		{method: http.MethodGet, path: server.GitHubWebhookPath, want: "protected"},
		{method: http.MethodGet, path: portforward.EndpointPath, want: "forward"},
		{method: http.MethodPost, path: "/public/api/v1/projects", want: "protected"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Body.String() != test.want {
			t.Fatalf("%s %s reached %q, want %q", test.method, test.path, response.Body.String(), test.want)
		}
	}
}

func TestAdminHostnameHandlerSplitsAdminAndPublicPaths(t *testing.T) {
	marker := func(value string) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
	}
	handler := adminHostnameHandler(marker("admin"), marker("public"))

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/", want: "admin"},
		{path: "/api/v1/settings/installation", want: "admin"},
		{path: "/public/api/v1/projects", want: "public"},
		{path: "/public/mcp", want: "public"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Body.String() != test.want {
			t.Fatalf("%s reached %q, want %q", test.path, response.Body.String(), test.want)
		}
	}
}
