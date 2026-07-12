package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type apiTokenRepositoryStub struct {
	tokens     []state.APIToken
	lastSecret string
}

func (repository *apiTokenRepositoryStub) APITokens(context.Context) ([]state.APIToken, error) {
	return repository.tokens, nil
}

func (repository *apiTokenRepositoryStub) CreateAPIToken(_ context.Context, input state.CreateAPIToken, secret string) (state.APIToken, error) {
	repository.lastSecret = secret
	repository.tokens = append(repository.tokens, input.APIToken)
	return input.APIToken, nil
}

func (repository *apiTokenRepositoryStub) RevokeAPIToken(_ context.Context, input state.RevokeAPIToken) error {
	for index := range repository.tokens {
		if repository.tokens[index].ID == input.ID && repository.tokens[index].RevokedAtMillis == nil {
			repository.tokens[index].RevokedAtMillis = &input.RevokedAtMillis
			return nil
		}
	}
	return state.ErrAPITokenNotFound
}

func TestAPITokenAdminAPIReturnsSecretOnceAndRevokes(t *testing.T) {
	repository := &apiTokenRepositoryStub{}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithAPITokens(repository)),
	)
	create := projectRequest(http.MethodPost, "/api/v1/tokens", `{"name":"deploy-bot","role":"admin"}`)
	create.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create token = %d/%s", response.Code, response.Body)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	publicID, secret, err := apitoken.Parse(created.Token)
	if err != nil || publicID != created.ID || secret != repository.lastSecret {
		t.Fatalf("created token format = %q/%q, %v", publicID, secret, err)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/tokens", ""))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "ptk_") || strings.Contains(response.Body.String(), repository.lastSecret) {
		t.Fatalf("listed token leaked secret: %d/%s", response.Code, response.Body)
	}

	revoke := projectRequest(http.MethodDelete, "/api/v1/tokens/"+created.ID, "")
	revoke.Header.Set("Origin", "https://admin.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, revoke)
	if response.Code != http.StatusNoContent || repository.tokens[0].RevokedAtMillis == nil {
		t.Fatalf("revoke token = %d/%s, %+v", response.Code, response.Body, repository.tokens)
	}
}
