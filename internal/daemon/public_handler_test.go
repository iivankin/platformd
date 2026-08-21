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
		{method: http.MethodPost, path: "/public/api/v1/hosts/otlp/v1/logs", want: "hosts"},
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

func TestPrivateHostHandlerAllowsOnlyPrivatePeers(t *testing.T) {
	handler := privateHostHandler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("hosts"))
	}))

	for _, test := range []struct {
		remote string
		code   int
	}{
		{remote: "10.20.0.4:12345", code: http.StatusOK},
		{remote: "127.0.0.1:12345", code: http.StatusOK},
		{remote: "[fd00::4]:12345", code: http.StatusOK},
		{remote: "203.0.113.10:12345", code: http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodGet, "/public/api/v1/hosts/connect", nil)
		request.RemoteAddr = test.remote
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("remote %s status = %d, want %d", test.remote, response.Code, test.code)
		}
	}
}
