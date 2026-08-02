package server

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObserveServerErrorsRecordsRouteCodeAndCause(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	handler := observeServerErrors(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Request-ID", "request-1")
		writeAPIError(response, http.StatusInternalServerError, "store_failed", "Unable to load state", errors.New("database unavailable"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/projects/project", nil))

	message := output.String()
	if !strings.Contains(message, `event=admin_api_request_failed`) ||
		!strings.Contains(message, `path="/api/v1/projects/project"`) ||
		!strings.Contains(message, `error_code="store_failed"`) ||
		!strings.Contains(message, `request_id="request-1"`) ||
		!strings.Contains(message, `error="database unavailable"`) {
		t.Fatalf("event = %q", message)
	}
}

func TestObserveServerErrorsAddsCorrelationIDBeforeFiveHundredResponse(t *testing.T) {
	handler := observeServerErrors(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load state")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/state", nil))
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("500 response has no correlation ID")
	}
}
