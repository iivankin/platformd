package mcp_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/mcp"
	"github.com/iivankin/platformd/internal/state"
)

func TestRevokedBearerTokenCannotInitializeNextMCPRequest(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := apitoken.NewVerifier(master)
	if err != nil {
		t.Fatal(err)
	}
	const publicID = "018bcfe5-687b-7fff-bfff-ffffffffffff"
	value, secret, err := apitoken.Generate(publicID, bytes.NewReader(bytes.Repeat([]byte{0x29}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	if _, err := store.CreateAPIToken(ctx, state.CreateAPIToken{
		APIToken: state.APIToken{
			ID: publicID, Name: "agent", Role: "read", ProjectID: &projectID,
			SecretHMAC: verifier.Digest(publicID, secret), CreatedAtMillis: 2,
		},
		AuditEventID: "token-audit", ActorID: "actor", ActorEmail: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	mcpHandler, err := mcp.New(mcp.Config{Hostname: "api.example.com", Version: "test", Repository: store})
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := automationauth.New(automationauth.Config{
		Store: store, Verifier: verifier, Limiter: automationauth.NewInMemoryFailureLimiter(),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := authenticator.Protect(mcpHandler)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initializeRequest(value))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("authenticated initialize = %d/%s", response.Code, response.Body)
	}
	if err := store.RevokeAPIToken(ctx, state.RevokeAPIToken{
		ID: publicID, AuditEventID: "revoke-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", RevokedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, initializeRequest(value))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked initialize status = %d/%s", response.Code, response.Body)
	}
}

func initializeRequest(token string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}`
	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/mcp", strings.NewReader(body))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}
