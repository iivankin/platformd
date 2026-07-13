package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAvailabilityHandlerBlocksDisabledTarget(t *testing.T) {
	called := 0
	handler, err := newAvailabilityHandler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called++
		response.WriteHeader(http.StatusNoContent)
	}), false)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://resource.example.com/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || called != 0 ||
		response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("closed response = %d headers=%v called=%d", response.Code, response.Header(), called)
	}

	handler, err = newAvailabilityHandler(handler.target, true)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("enabled response = %d called=%d", response.Code, called)
	}
}
