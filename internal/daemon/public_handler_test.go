//go:build !platformd_worker

package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iivankin/platformd/internal/portforward"
)

func TestPublicHandlerExposesOnlyExactPublicEndpoints(t *testing.T) {
	marker := func(value string) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
	}
	handler := publicHandler(
		marker("image"),
		marker("forward"),
		marker("create-forward"),
		marker("hosts"),
		marker("protected"),
	)

	for _, test := range []struct {
		method string
		path   string
		want   string
	}{
		{method: http.MethodPost, path: "/public/api/v1/projects/shop/services/api/image", want: "image"},
		{method: http.MethodGet, path: "/public/api/v1/projects/shop/services/api/image", want: "image"},
		{method: http.MethodPut, path: "/public/api/v1/projects/shop/services/api/image", want: "protected"},
		{method: http.MethodGet, path: portforward.EndpointPath, want: "forward"},
		{
			method: http.MethodPost,
			path:   "/public/api/v1/projects/shop/resources/api/port-forwards",
			want:   "create-forward",
		},
		{method: http.MethodPost, path: "/public/api/v1/hosts/join", want: "hosts"},
		{method: http.MethodGet, path: "/public/api/v1/hosts/connect", want: "hosts"},
		{method: http.MethodGet, path: "/public/api/v1/hosts/tunnel", want: "hosts"},
		{method: http.MethodGet, path: "/public/api/v1/hosts/images/revision", want: "hosts"},
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
