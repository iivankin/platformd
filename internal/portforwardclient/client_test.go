package portforwardclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestValidateKeepsTicketOutOfInsecureOrDecoratedURLs(t *testing.T) {
	valid := Config{
		URL:    "wss://admin.example.com/public/api/v1/port-forward",
		Ticket: "pft_secret", LocalPort: 5432,
	}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	tests := []Config{
		{URL: "ws://admin.example.com/public/api/v1/port-forward", Ticket: valid.Ticket, LocalPort: valid.LocalPort},
		{URL: valid.URL + "?ticket=pft_secret", Ticket: valid.Ticket, LocalPort: valid.LocalPort},
		{URL: valid.URL, LocalPort: valid.LocalPort},
		{URL: valid.URL, Ticket: valid.Ticket, LocalPort: 0},
	}
	for _, test := range tests {
		if err := validate(test); err == nil {
			t.Fatalf("accepted unsafe configuration: %+v", test)
		}
	}
}

func TestHTTPProxyPreservesPathAndSetsVirtualHost(t *testing.T) {
	var receivedHost, receivedPath string
	proxy := httpProxy("errors-api.shop.internal", roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedHost = request.Host
		receivedPath = request.URL.RequestURI()
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("accepted")),
		}, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9001/api/1/envelope/?v=1", strings.NewReader("event"))
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || receivedHost != "errors-api.shop.internal" || receivedPath != "/api/1/envelope/?v=1" {
		t.Fatalf("proxied response/host/path = %d / %q / %q", response.Code, receivedHost, receivedPath)
	}
}

func TestValidateAcceptsOnlyBareHTTPVirtualHosts(t *testing.T) {
	valid := Config{
		URL:    "wss://admin.example.com/public/api/v1/port-forward",
		Ticket: "pft_secret", LocalPort: 9001, HTTPHost: "errors-api.shop.internal",
	}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"errors-api.shop.internal:9001", "http://errors-api.shop.internal", "errors api"} {
		candidate := valid
		candidate.HTTPHost = host
		if err := validate(candidate); err == nil {
			t.Fatalf("accepted invalid HTTP host %q", host)
		}
	}
}
