package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestServiceOTLPGatewayInjectsTrustedIdentity(t *testing.T) {
	var receivedServiceID, receivedBody string
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedServiceID = request.Header.Get("X-Platformd-Service-Id")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read forwarded OTLP body: %v", err)
			return
		}
		receivedBody = string(body)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	target, err := url.Parse(receiver.URL)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &serviceGateway{
		otlpProxy: httputil.NewSingleHostReverseProxy(target),
		otlpRoutes: map[string]serviceRoute{
			"otel-api.shop.internal": {serviceID: "service-1"},
		},
	}

	request := httptest.NewRequest(http.MethodPost, "http://otel-api.shop.internal:4318/v1/traces", strings.NewReader("payload"))
	request.Header.Set("X-Platformd-Service-Id", "spoofed-service")
	response := httptest.NewRecorder()
	gateway.serveOTLP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if receivedServiceID != "service-1" {
		t.Fatalf("forwarded service identity = %q", receivedServiceID)
	}
	if receivedBody != "payload" {
		t.Fatalf("forwarded body = %q", receivedBody)
	}
}

func TestServiceOTLPGatewayRejectsWrongSurface(t *testing.T) {
	gateway := &serviceGateway{otlpRoutes: map[string]serviceRoute{
		"otel-api.shop.internal": {serviceID: "service-1"},
	}}
	for _, target := range []string{
		"http://errors-api.shop.internal:4318/v1/traces",
		"http://otel-api.shop.internal:4318/api/1/envelope/",
	} {
		request := httptest.NewRequest(http.MethodPost, target, nil)
		response := httptest.NewRecorder()
		gateway.serveOTLP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", target, response.Code, http.StatusNotFound)
		}
	}
}
