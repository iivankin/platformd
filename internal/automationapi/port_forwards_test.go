package automationapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type oidcVerifierStub struct {
	identity portforward.OIDCIdentity
	err      error
}

func (stub oidcVerifierStub) Verify(
	_ context.Context,
	_, _, _ string,
	_ []string,
) (portforward.OIDCIdentity, error) {
	return stub.identity, stub.err
}

type servicePortForwardRepository struct{}

func (servicePortForwardRepository) ResolveProject(_ context.Context, name string) (portforward.ResolvedProject, error) {
	return portforward.ResolvedProject{ID: "project", Name: name}, nil
}

func (servicePortForwardRepository) ResolveResource(_ context.Context, _ string, name string) (portforward.ResolvedResource, error) {
	return portforward.ResolvedResource{
		ID: "service-id", Kind: "service", Name: name,
		PortForward: &serviceconfig.PortForward{Repository: "acme/api"},
	}, nil
}

func (servicePortForwardRepository) ResolveResourceAddress(string, string, string, int) (string, error) {
	return "10.42.0.4:8080", nil
}

func (servicePortForwardRepository) RecordPortForwardTicket(context.Context, portforward.AuditRecord) error {
	return nil
}

type emptyTokenStore struct{}

func (emptyTokenStore) APITokenCredential(context.Context, string) (state.APIToken, error) {
	return state.APIToken{}, errors.New("missing token")
}

func mustAuthenticator(t *testing.T) *automationauth.Authenticator {
	t.Helper()
	authenticator, err := automationauth.New(automationauth.Config{
		Store:   emptyTokenStore{},
		Limiter: automationauth.NewInMemoryFailureLimiter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

func TestPortForwardCreateHandlerIssuesTicketWithOIDC(t *testing.T) {
	application, err := portforward.New(portforward.Config{
		Repository: servicePortForwardRepository{},
		Resolver:   servicePortForwardRepository{},
		Audit:      servicePortForwardRepository{},
		OIDC: oidcVerifierStub{identity: portforward.OIDCIdentity{Repository: "acme/api", RunID: "99"}},
		NewID: func() (string, error) { return "port-forward-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := CreatePortForwardHandler(PortForwardCreateConfig{
		Hostname:      "admin.example.com",
		Application:   application,
		Authenticator: mustAuthenticator(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /public/api/v1/projects/{projectName}/resources/{resourceName}/port-forwards", handler)

	request := httptest.NewRequest(
		http.MethodPost,
		"https://admin.example.com/public/api/v1/projects/shop/resources/api/port-forwards",
		strings.NewReader(`{"port":8080,"localPort":18080,"expiresInSeconds":600}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0.sig")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusCreated {
		t.Fatalf("create port forward = %d/%s", response.Code, body)
	}
	for _, expected := range []string{
		`"id":"port-forward-id"`, `"project":"shop"`, `"resource":"api"`, `"resourceKind":"service"`, `"ticket":"pft_`,
		"wss://admin.example.com/public/api/v1/port-forward", "--local-port 18080",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response does not contain %q: %s", expected, body)
		}
	}
}

func TestPortForwardCreateHandlerRejectsMissingAuth(t *testing.T) {
	application, err := portforward.New(portforward.Config{
		Repository: servicePortForwardRepository{},
		Resolver:   servicePortForwardRepository{},
		Audit:      servicePortForwardRepository{},
		OIDC:       oidcVerifierStub{},
		NewID:      func() (string, error) { return "port-forward-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := CreatePortForwardHandler(PortForwardCreateConfig{
		Hostname:      "admin.example.com",
		Application:   application,
		Authenticator: mustAuthenticator(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /public/api/v1/projects/{projectName}/resources/{resourceName}/port-forwards", handler)
	request := httptest.NewRequest(
		http.MethodPost,
		"https://admin.example.com/public/api/v1/projects/shop/resources/api/port-forwards",
		strings.NewReader(`{"port":8080}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth = %d/%s", response.Code, response.Body)
	}
}
