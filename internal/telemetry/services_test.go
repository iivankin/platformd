package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestQueryServiceReturnsTelemetryErrorDetails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"code":"invalid_request","message":"invalid metric SQL: unknown column queue"}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{process: &Process{client: server.Client(), target: target}}
	_, err = manager.QueryService(context.Background(), "service", http.MethodGet, "/issues", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid_request: invalid metric SQL: unknown column queue") {
		t.Fatalf("query error = %v", err)
	}
}

func TestQueryMetricScopeSendsResolvedServicesAndQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/metric-scopes/query" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			ServiceIDs []string        `json:"serviceIds"`
			Query      json.RawMessage `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body.ServiceIDs, []string{"service-one", "service-two"}) ||
			string(body.Query) != `{"sql":"SELECT * FROM metrics"}` {
			t.Errorf("metric scope body = %#v, %s", body.ServiceIDs, body.Query)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = response.Write([]byte(`{"error":"invalid SQL"}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{process: &Process{client: server.Client(), target: target}}
	result, err := manager.QueryMetricScope(context.Background(), []string{"service-one", "service-two"},
		"query", json.RawMessage(`{"sql":"SELECT * FROM metrics"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusUnprocessableEntity || result.ContentType != "application/json" ||
		string(result.Body) != `{"error":"invalid SQL"}` {
		t.Fatalf("metric scope response = %+v", result)
	}
}
