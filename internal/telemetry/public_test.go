package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

type publicRepositoryStub struct {
	sentry state.ServiceDesired
	traces state.ServiceDesired
}

func (repository publicRepositoryStub) ServiceBySentryHostname(context.Context, string) (state.ServiceDesired, error) {
	return repository.sentry, nil
}

func (repository publicRepositoryStub) ServiceByOTLPTraceHostname(context.Context, string) (state.ServiceDesired, error) {
	return repository.traces, nil
}

func TestPublicHandlerResolvesOTLPTraceHostname(t *testing.T) {
	var serviceID, body string
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		serviceID = request.Header.Get("X-Platformd-Service-Id")
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		body = string(data)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	target, err := url.Parse(receiver.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewPublicHandler(
		publicRepositoryStub{traces: state.ServiceDesired{ID: "trace-service"}},
		&ServiceManager{otlpProxy: httputil.NewSingleHostReverseProxy(target)},
	)
	request := httptest.NewRequest(http.MethodPost, "https://otel.example.com/v1/traces", strings.NewReader("trace"))
	request.Host = "otel.example.com"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || serviceID != "trace-service" || body != "trace" {
		t.Fatalf("public OTLP handler = status %d, service %q, body %q", response.Code, serviceID, body)
	}
}
