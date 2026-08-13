package sentry

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type targetResolverFunc func(string) (*url.URL, bool)

func (resolve targetResolverFunc) Target(serviceID string) (*url.URL, bool) {
	return resolve(serviceID)
}

func TestDataPlanePathAllowed(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"/public/api/v1/apps":                    false,
		"/public/mcp":                            false,
		"/api/project/envelope/":                 true,
		"/api/project/minidump/":                 true,
		"/api/0/organizations/org/chunk-upload/": true,
		"/api/v1/apps":                           false,
		"/health":                                false,
		"/public/mcp/tools":                      false,
	}
	for path, allowed := range tests {
		if actual := DataPlanePathAllowed(path); actual != allowed {
			t.Errorf("DataPlanePathAllowed(%q) = %t, want %t", path, actual, allowed)
		}
	}
}

func TestForwardedClientAddress(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("POST", "http://errors.example.com/api/1/envelope/", nil)
	request.RemoteAddr = "10.0.0.4:42110"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	if actual := forwardedClientAddress(request); actual != "203.0.113.9" {
		t.Fatalf("forwardedClientAddress() = %q, want public ingress address", actual)
	}

	request.Header.Set("X-Forwarded-For", "attacker, 203.0.113.9")
	if actual := forwardedClientAddress(request); actual != "10.0.0.4" {
		t.Fatalf("forwardedClientAddress() = %q, want direct peer for invalid chain", actual)
	}
}

func TestPublicClientAddressIgnoresForwardedHeader(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("POST", "https://errors.example.com/api/1/envelope/", nil)
	request.RemoteAddr = "192.0.2.7:42110"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("X-Forwarded-For", "198.51.100.42")

	if actual := publicClientAddress(request); actual != "203.0.113.9" {
		t.Fatalf("publicClientAddress() = %q, want Cloudflare client address", actual)
	}

	request.Header.Set("CF-Connecting-IP", "invalid")
	if actual := publicClientAddress(request); actual != "192.0.2.7" {
		t.Fatalf("publicClientAddress() = %q, want direct peer", actual)
	}
}

func TestProxyPreservesValidatedClientAddress(t *testing.T) {
	t.Parallel()
	var forwarded string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forwarded = request.Header.Get("X-Forwarded-For")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxy(targetResolverFunc(func(string) (*url.URL, bool) { return target, true }), func(context.Context, string, string) bool { return false }, func(string, []byte) {})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()
	request := httptest.NewRequest("POST", "http://errors.example.com/api/1/envelope/", nil)
	request.RemoteAddr = "10.0.0.4:42110"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	response := httptest.NewRecorder()

	proxy.Serve(response, request, "tracker")

	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy response = %d, want %d", response.Code, http.StatusNoContent)
	}
	if forwarded != "203.0.113.9" {
		t.Fatalf("forwarded address = %q, want public ingress address", forwarded)
	}
}

func TestProxyOnlyForwardsCloudflareCountryFromPublicIngress(t *testing.T) {
	t.Parallel()
	countries := make([]string, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		countries = append(countries, request.Header.Get("Cf-IpCountry"))
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxy(targetResolverFunc(func(string) (*url.URL, bool) { return target, true }), func(context.Context, string, string) bool { return false }, func(string, []byte) {})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()

	internal := httptest.NewRequest("POST", "http://errors.internal/api/1/envelope/", nil)
	internal.Header.Set("Cf-IpCountry", "US")
	proxy.Serve(httptest.NewRecorder(), internal, "tracker")
	public := httptest.NewRequest("POST", "https://errors.example.com/api/1/envelope/", nil)
	public.Header.Set("Cf-IpCountry", "RS")
	proxy.ServePublic(httptest.NewRecorder(), public, "tracker")

	if len(countries) != 2 || countries[0] != "" || countries[1] != "RS" {
		t.Fatalf("forwarded Cloudflare countries = %v", countries)
	}
}

func TestPublicProxyReplacesSpoofedForwardedAddress(t *testing.T) {
	t.Parallel()
	var forwarded string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forwarded = request.Header.Get("X-Forwarded-For")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxy(targetResolverFunc(func(string) (*url.URL, bool) { return target, true }), func(context.Context, string, string) bool { return false }, func(string, []byte) {})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()
	request := httptest.NewRequest("POST", "https://errors.example.com/api/1/envelope/", nil)
	request.RemoteAddr = "192.0.2.7:42110"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("X-Forwarded-For", "198.51.100.42")

	proxy.ServePublic(httptest.NewRecorder(), request, "tracker")

	if forwarded != "203.0.113.9" {
		t.Fatalf("forwarded address = %q, want Cloudflare client address", forwarded)
	}
}

func TestProxyConsumesTrustedTelemetryNotifications(t *testing.T) {
	t.Parallel()
	payload := []byte(`[{"eventType":"issue_created"}]`)
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set(webhookEventsHeader, base64.RawURLEncoding.EncodeToString(payload))
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	var receivedService string
	var receivedPayload []byte
	proxy, err := NewProxy(
		targetResolverFunc(func(string) (*url.URL, bool) { return target, true }),
		func(context.Context, string, string) bool { return false },
		func(serviceID string, value []byte) {
			receivedService = serviceID
			receivedPayload = value
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()
	response := httptest.NewRecorder()
	proxy.Serve(response, httptest.NewRequest(http.MethodPost, "http://errors.internal/api/1/envelope/", nil), "service")

	if response.Header().Get(webhookEventsHeader) != "" {
		t.Fatal("private notification header leaked to the client")
	}
	if receivedService != "service" || string(receivedPayload) != string(payload) {
		t.Fatalf("notification = %q/%s", receivedService, receivedPayload)
	}
}

func TestInternalArtifactUploadDoesNotRequireToken(t *testing.T) {
	t.Parallel()
	var authorized string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorized = request.Header.Get("X-Platformd-Artifact-Authorized")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxy(
		targetResolverFunc(func(string) (*url.URL, bool) { return target, true }),
		func(context.Context, string, string) bool { return false },
		func(string, []byte) {},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()

	response := httptest.NewRecorder()
	proxy.Serve(response, httptest.NewRequest(
		http.MethodPost,
		"http://errors.internal/api/0/organizations/platformd/chunk-upload/",
		nil,
	), "service")

	if response.Code != http.StatusNoContent {
		t.Fatalf("internal artifact response = %d, want %d", response.Code, http.StatusNoContent)
	}
	if authorized != "1" {
		t.Fatalf("trusted artifact header = %q, want 1", authorized)
	}
}

func TestPublicArtifactUploadRequiresServiceToken(t *testing.T) {
	t.Parallel()
	var authorized string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorized = request.Header.Get("X-Platformd-Artifact-Authorized")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxy(
		targetResolverFunc(func(string) (*url.URL, bool) { return target, true }),
		func(_ context.Context, serviceID, token string) bool {
			return serviceID == "service" && token == "secret"
		},
		func(string, []byte) {},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.transport.CloseIdleConnections()
	requestURL := "https://errors.example.com/api/0/organizations/platformd/chunk-upload/"

	unauthorized := httptest.NewRecorder()
	proxy.ServePublic(unauthorized, httptest.NewRequest(http.MethodPost, requestURL, nil), "service")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("public artifact response = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, requestURL, nil)
	authorizedRequest.Header.Set("Authorization", "Bearer secret")
	success := httptest.NewRecorder()
	proxy.ServePublic(success, authorizedRequest, "service")
	if success.Code != http.StatusNoContent {
		t.Fatalf("authorized public artifact response = %d, want %d", success.Code, http.StatusNoContent)
	}
	if authorized != "1" {
		t.Fatalf("trusted artifact header = %q, want 1", authorized)
	}
}
