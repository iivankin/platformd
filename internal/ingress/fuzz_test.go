package ingress

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func FuzzProxyInputs(f *testing.F) {
	router, err := New(Config{
		AdminHostname: "admin.example.com",
		AdminHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("admin.example.com", "admin.example.com", "192.0.2.1:1234", "203.0.113.1", uint8(0), true)
	f.Add("bad host", "other.example.com", "malformed", "invalid", uint8(101), false)

	f.Fuzz(func(t *testing.T, host, sni, remoteAddress, connectingIP string, headerCount uint8, withTLS bool) {
		if len(host)+len(sni)+len(remoteAddress)+len(connectingIP) > 128<<10 {
			t.Skip()
		}
		request := &http.Request{
			Method:     http.MethodGet,
			URL:        &url.URL{Scheme: "https", Host: host, Path: "/"},
			Host:       host,
			Header:     make(http.Header),
			RemoteAddr: remoteAddress,
		}
		if withTLS {
			request.TLS = &tls.ConnectionState{ServerName: sni}
		}
		request.Header.Set("CF-Connecting-IP", connectingIP)
		for index := 0; index < int(headerCount); index++ {
			request.Header.Add("X-Fuzz", strconv.Itoa(index))
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		switch response.Code {
		case http.StatusNoContent, http.StatusMisdirectedRequest, http.StatusRequestHeaderFieldsTooLarge:
		default:
			t.Fatalf("unexpected proxy status %d", response.Code)
		}
	})
}
