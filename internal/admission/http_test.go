package admission

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPMutationAdmissionAllowsReadsAndExemptsUpdateRoute(t *testing.T) {
	t.Parallel()
	gate := New()
	update, _, err := gate.TryUpdate()
	if err != nil {
		t.Fatal(err)
	}
	defer update.Release()
	calls := 0
	handler := WrapHTTPMutations(gate, "admin_request", "/api/v1/infrastructure/update", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/infrastructure/update", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("allowed %s %s = %d", request.Method, request.URL.Path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/projects", nil))
	if response.Code != http.StatusConflict || calls != 2 {
		t.Fatalf("blocked mutation = %d/%s, calls=%d", response.Code, response.Body, calls)
	}
}
