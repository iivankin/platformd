package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/server"
)

func TestAutomationHandlerExposesOnlyExactPublicEndpoints(t *testing.T) {
	t.Parallel()
	handler := automationHandler(
		markerHandler("github"),
		markerHandler("forward"),
		markerHandler("protected"),
	)
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{method: http.MethodPost, path: server.GitHubWebhookPath, want: "github"},
		{method: http.MethodGet, path: server.GitHubWebhookPath, want: "protected"},
		{method: http.MethodGet, path: portforward.EndpointPath, want: "forward"},
		{method: http.MethodPost, path: "/api/v1/projects", want: "protected"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if got := response.Header().Get("X-Test-Handler"); got != test.want {
				t.Fatalf("handler = %q, want %q", got, test.want)
			}
		})
	}
}

func markerHandler(value string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Test-Handler", value)
		response.WriteHeader(http.StatusNoContent)
	})
}
