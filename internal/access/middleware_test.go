package access_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iivankin/platformd/internal/access"
)

type verifierStub struct {
	identity access.Identity
	err      error
}

func (verifier verifierStub) Verify(context.Context, string) (access.Identity, error) {
	return verifier.identity, verifier.err
}

func TestProtectAdminAuthenticatesExactHostAndIdentity(t *testing.T) {
	t.Parallel()

	handler := access.ProtectAdmin("admin.example.com", verifierStub{identity: access.Identity{Subject: "subject", Email: "admin@example.com"}}, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok || identity.Subject != "subject" {
			t.Fatalf("identity = %+v, %v", identity, ok)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	request := adminRequest(http.MethodPost, "/api/v1/projects")
	request.Header.Set("Cf-Access-Jwt-Assertion", "token")
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestProtectAdminRejectsMissingTokenCSRFAndHostMismatch(t *testing.T) {
	t.Parallel()

	handler := access.ProtectAdmin("admin.example.com", verifierStub{identity: access.Identity{Subject: "subject", Email: "admin@example.com"}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was reached")
	}))
	tests := []struct {
		name    string
		request *http.Request
		status  int
	}{
		{name: "missing token", request: adminRequest(http.MethodGet, "/"), status: http.StatusForbidden},
		{name: "missing origin", request: authenticatedRequest(http.MethodPost, "/api/v1/projects"), status: http.StatusForbidden},
		{name: "wrong host", request: requestWithHost(http.MethodGet, "/", "other.example.com"), status: http.StatusMisdirectedRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, test.request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestProtectAdminAllowsOnlyLoopbackHealthWithoutAccess(t *testing.T) {
	t.Parallel()

	handler := access.ProtectAdmin("admin.example.com", verifierStub{err: errors.New("must not verify")}, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	loopback := adminRequest(http.MethodGet, "/healthz")
	loopback.RemoteAddr = "127.0.0.1:42100"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loopback)
	if response.Code != http.StatusNoContent {
		t.Fatalf("loopback status = %d", response.Code)
	}

	remote := adminRequest(http.MethodGet, "/healthz")
	remote.RemoteAddr = "203.0.113.1:42100"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, remote)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d", response.Code)
	}
}

func authenticatedRequest(method, path string) *http.Request {
	request := adminRequest(method, path)
	request.Header.Set("Cf-Access-Jwt-Assertion", "token")
	return request
}

func adminRequest(method, path string) *http.Request {
	return requestWithHost(method, path, "admin.example.com")
}

func requestWithHost(method, path, host string) *http.Request {
	request := httptest.NewRequest(method, "https://"+host+path, nil)
	request.Host = host
	request.TLS = &tls.ConnectionState{ServerName: host}
	return request
}
