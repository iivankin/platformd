package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/server"
)

func TestMetaContract(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	response := httptest.NewRecorder()
	server.Handler(server.DefaultMeta("bootstrapping")).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var meta server.Meta
	if err := json.NewDecoder(response.Body).Decode(&meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.Version == "" || meta.OS == "" || meta.Architecture == "" {
		t.Fatalf("incomplete meta: %+v", meta)
	}
}

func TestSPAFallbackDoesNotHideMissingAPI(t *testing.T) {
	t.Parallel()

	handler := server.Handler(server.DefaultMeta("bootstrapping"))

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/projects/example", nil))
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("SPA fallback status/body = %d/%q", pageResponse.Code, pageResponse.Body.String())
	}

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("missing API status = %d, want 404", apiResponse.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	server.Handler(server.DefaultMeta("bootstrapping")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") || strings.Contains(got, "unsafe-inline") {
		t.Fatalf("unexpected CSP: %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}
