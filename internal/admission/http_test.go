package admission

import (
	"errors"
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
	handler := WrapHTTPMutations(gate, "admin_request", "/api/v1/infrastructure/update", nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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

func TestHTTPMutationAdmissionCanReserveAnExclusiveProjectDeletion(t *testing.T) {
	t.Parallel()
	gate := New()
	active, err := gate.Begin("deployment", "active")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := WrapHTTPMutations(gate, "admin_request", "", func(request *http.Request) bool {
		return request.Method == http.MethodDelete && request.URL.Path == "/api/v1/projects/project"
	}, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls++
		if _, err := gate.Begin("deployment", "nested"); !errors.Is(err, ErrUpdating) {
			t.Fatalf("nested admission error = %v", err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))

	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, httptest.NewRequest(http.MethodDelete, "/api/v1/projects/project", nil))
	if blocked.Code != http.StatusConflict || calls != 0 {
		t.Fatalf("blocked deletion = %d/%s, calls=%d", blocked.Code, blocked.Body, calls)
	}
	active.Release()

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, httptest.NewRequest(http.MethodDelete, "/api/v1/projects/project", nil))
	if allowed.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("allowed deletion = %d/%s, calls=%d", allowed.Code, allowed.Body, calls)
	}
}
