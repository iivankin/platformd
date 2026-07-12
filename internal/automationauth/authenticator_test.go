package automationauth_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type credentialStoreStub struct {
	token state.APIToken
}

func (store credentialStoreStub) APITokenCredential(context.Context, string) (state.APIToken, error) {
	return store.token, nil
}

type limiterStub struct {
	allowed    bool
	retryAfter time.Duration
	failed     int
	succeeded  int
	publicID   string
	source     string
}

func (limiter *limiterStub) Permit(publicID, source string) (bool, time.Duration) {
	limiter.publicID = publicID
	limiter.source = source
	return limiter.allowed, limiter.retryAfter
}

func (limiter *limiterStub) Failed(string, string) {
	limiter.failed++
}

func (limiter *limiterStub) Succeeded(string, string) {
	limiter.succeeded++
}

func TestAutomationAuthenticatorVerifiesBearerAndSetsScopedIdentity(t *testing.T) {
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := apitoken.NewVerifier(master)
	if err != nil {
		t.Fatal(err)
	}
	const publicID = "018bcfe5-687b-7fff-bfff-ffffffffffff"
	value, secret, err := apitoken.Generate(publicID, bytes.NewReader(bytes.Repeat([]byte{0x21}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	limiter := &limiterStub{allowed: true}
	authenticator, err := automationauth.New(automationauth.Config{
		Store: credentialStoreStub{token: state.APIToken{
			ID: publicID, Role: "admin", ProjectID: &projectID,
			SecretHMAC: verifier.Digest(publicID, secret),
		}},
		Verifier: verifier, Limiter: limiter,
	})
	if err != nil {
		t.Fatal(err)
	}
	protected := authenticator.Protect(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identity, ok := automation.IdentityFromContext(request.Context())
		if !ok || identity.TokenID != publicID || identity.ProjectID == nil || *identity.ProjectID != projectID || !identity.IsAdmin() {
			t.Fatalf("automation identity = %+v, present=%t", identity, ok)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/mcp", nil)
	request.RemoteAddr = "192.0.2.5:1234"
	request.Header.Set("Authorization", "Bearer "+value)
	request.Header.Set("CF-Connecting-IP", "203.0.113.8")
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || limiter.failed != 0 || limiter.succeeded != 1 || limiter.publicID != publicID || limiter.source != "203.0.113.8" {
		t.Fatalf("authenticated response = %d, limiter=%+v", response.Code, limiter)
	}
}

func TestAutomationAuthenticatorRejectsInvalidAndRateLimitedRequests(t *testing.T) {
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := apitoken.NewVerifier(master)
	if err != nil {
		t.Fatal(err)
	}
	limiter := &limiterStub{allowed: true}
	authenticator, err := automationauth.New(automationauth.Config{
		Store:    credentialStoreStub{token: state.APIToken{ID: "token", Role: "read", SecretHMAC: bytes.Repeat([]byte{0x55}, 32)}},
		Verifier: verifier, Limiter: limiter,
	})
	if err != nil {
		t.Fatal(err)
	}
	protected := authenticator.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid request reached protected handler")
	}))
	request := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects", nil)
	request.Header.Set("Authorization", "Bearer malformed")
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || limiter.failed != 1 || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("invalid response = %d, limiter=%+v", response.Code, limiter)
	}

	limiter.allowed = false
	limiter.retryAfter = 1500 * time.Millisecond
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" || limiter.failed != 1 {
		t.Fatalf("limited response = %d/%s, limiter=%+v", response.Code, response.Header().Get("Retry-After"), limiter)
	}
}
