package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
)

type recoveryAdminHandler struct {
	target http.Handler
}

func (handler recoveryAdminHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if recoveryAdminRequestAllowed(request.Method, request.URL.Path) {
		handler.target.ServeHTTP(response, request)
		return
	}
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{
			"code":    "recovery_in_progress",
			"message": "Only recovery actions are available until disaster recovery completes",
		},
	})
}

func recoveryAdminRequestAllowed(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return true
	}
	if method == http.MethodPost && path == "/api/v1/server/terminal-token" {
		return true
	}
	if path == "/api/v1/backups/target" && (method == http.MethodPut || method == http.MethodDelete) {
		return true
	}
	if method == http.MethodPost && path == "/api/v1/recovery/retry" {
		return true
	}
	return method == http.MethodPost && strings.HasPrefix(path, "/api/v1/backups/resources/") &&
		strings.HasSuffix(path, "/restore")
}
