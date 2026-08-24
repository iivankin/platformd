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

func (repository publicRepositoryStub) ServiceByOTLPHostname(context.Context, string) (state.ServiceDesired, error) {
	return repository.traces, nil
}

func TestPublicHandlerResolvesOTLPHostnameForTracesAndLogs(t *testing.T) {
	var serviceID, body, path string
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		serviceID = request.Header.Get("X-Platformd-Service-Id")
		path = request.URL.Path
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
	for _, signal := range []string{"traces", "logs"} {
		request := httptest.NewRequest(http.MethodPost, "https://otel.example.com/v1/"+signal, strings.NewReader(signal))
		request.Host = "otel.example.com"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || serviceID != "trace-service" || body != signal || path != "/v1/"+signal {
			t.Fatalf("public OTLP %s handler = status %d, service %q, path %q, body %q", signal, response.Code, serviceID, path, body)
		}
	}
}
