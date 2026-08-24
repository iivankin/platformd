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

func TestServiceDSNUsesCurrentCUID2Identity(t *testing.T) {
	t.Parallel()
	const serviceID = "tz4a98xxat96iws9zmbrgj3a"
	if got, want := SentryDSN("https", "errors.example.com", serviceID), "https://"+serviceID+"@errors.example.com/1"; got != want {
		t.Fatalf("DSN = %q, want %q", got, want)
	}
}

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

func TestServeTraceScopeSendsEveryProjectService(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/trace-scopes/0123456789abcdef" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			AnchorServiceID string   `json:"anchorServiceId"`
			ServiceIDs      []string `json:"serviceIds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body.ServiceIDs, []string{"api", "worker"}) {
			t.Errorf("trace scope services = %#v", body.ServiceIDs)
		}
		if body.AnchorServiceID != "api" {
			t.Errorf("trace scope anchor = %q", body.AnchorServiceID)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"traceId":"0123456789abcdef","spans":[]}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{process: &Process{client: server.Client(), target: target}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/traces/0123456789abcdef", nil)
	manager.ServeTraceScope(recorder, request, "0123456789abcdef", "api", []string{"api", "worker"})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
}

func TestServeTraceListScopeSendsAnchorProjectServicesAndQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/trace-scopes" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.RawQuery != "limit=25&status=error&sort=slowest" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		var body struct {
			AnchorServiceID string   `json:"anchorServiceId"`
			ServiceIDs      []string `json:"serviceIds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.AnchorServiceID != "api" || !reflect.DeepEqual(body.ServiceIDs, []string{"api", "worker"}) {
			t.Errorf("trace list scope = %q %#v", body.AnchorServiceID, body.ServiceIDs)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[]`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{process: &Process{client: server.Client(), target: target}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/traces?limit=25&status=error&sort=slowest", nil)
	manager.ServeTraceListScope(recorder, request, "api", []string{"api", "worker"})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
}

func TestServeAIOverviewScopeSendsServicesAndQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/ai-scopes/overview" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.RawQuery != "from=1000&to=2000&step=1000" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		var body struct {
			AnchorServiceID *string  `json:"anchorServiceId"`
			ServiceIDs      []string `json:"serviceIds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body.ServiceIDs, []string{"api", "worker"}) {
			t.Errorf("AI overview services = %#v", body.ServiceIDs)
		}
		if body.AnchorServiceID == nil || *body.AnchorServiceID != "api" {
			t.Errorf("AI overview anchor = %#v", body.AnchorServiceID)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"summary":{},"activity":[],"usage":[],"models":[],"agents":[],"users":[],"latency":[]}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{process: &Process{client: server.Client(), target: target}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ai/overview?from=1000&to=2000&step=1000", nil)
	manager.ServeAIOverviewScope(recorder, request, "api", []string{"api", "worker"})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
}
