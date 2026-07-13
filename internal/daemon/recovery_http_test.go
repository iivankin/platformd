package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryAdminHandlerAllowsOnlyReadAndRecoverySurfaces(t *testing.T) {
	called := 0
	handler := recoveryAdminHandler{target: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called++
		response.WriteHeader(http.StatusNoContent)
	})}
	tests := []struct {
		method  string
		path    string
		allowed bool
	}{
		{method: http.MethodGet, path: "/api/v1/projects", allowed: true},
		{method: http.MethodPost, path: "/api/v1/server/terminal-token", allowed: true},
		{method: http.MethodPut, path: "/api/v1/backups/target", allowed: true},
		{method: http.MethodDelete, path: "/api/v1/backups/target", allowed: true},
		{method: http.MethodPost, path: "/api/v1/backups/resources/redis/id/restore", allowed: true},
		{method: http.MethodPost, path: "/api/v1/recovery/retry", allowed: true},
		{method: http.MethodPost, path: "/api/v1/services", allowed: false},
		{method: http.MethodPut, path: "/api/v1/backups/resources/redis/id/policy", allowed: false},
		{method: http.MethodPost, path: "/api/v1/infrastructure/update", allowed: false},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, "https://admin.example.com"+test.path, nil))
		if test.allowed && response.Code != http.StatusNoContent {
			t.Fatalf("%s %s status = %d", test.method, test.path, response.Code)
		}
		if !test.allowed && (response.Code != http.StatusConflict ||
			!strings.Contains(response.Body.String(), `"code":"recovery_in_progress"`)) {
			t.Fatalf("blocked %s %s = %d/%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if called != 6 {
		t.Fatalf("allowed target calls = %d, want 6", called)
	}
}
