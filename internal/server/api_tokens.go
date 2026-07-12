package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

const maximumAPITokenRequestBytes = 16 << 10

type APITokenRepository interface {
	APITokens(context.Context) ([]state.APIToken, error)
	CreateAPIToken(context.Context, state.CreateAPIToken, string) (state.APIToken, error)
	RevokeAPIToken(context.Context, state.RevokeAPIToken) error
}

type apiTokenResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	ProjectID  *string `json:"projectId,omitempty"`
	CreatedAt  int64   `json:"createdAt"`
	LastUsedAt *int64  `json:"lastUsedAt,omitempty"`
	RevokedAt  *int64  `json:"revokedAt,omitempty"`
	Token      string  `json:"token,omitempty"`
}

func registerAPITokenRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/tokens", listAPITokens(config.tokens))
	mux.HandleFunc("POST /api/v1/tokens", createAPIToken(config))
	mux.HandleFunc("DELETE /api/v1/tokens/{tokenID}", revokeAPIToken(config))
}

func listAPITokens(repository APITokenRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		tokens, err := repository.APITokens(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list API tokens")
			return
		}
		result := make([]apiTokenResponse, 0, len(tokens))
		for _, token := range tokens {
			result = append(result, publicAPIToken(token, ""))
		}
		writeJSON(response, http.StatusOK, map[string]any{"tokens": result})
	}
}

func createAPIToken(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Name      string  `json:"name"`
		Role      string  `json:"role"`
		ProjectID *string `json:"projectId"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeAPITokenJSON(response, request, &body) {
			return
		}
		if err := resourcename.Validate(body.Name); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_name", err.Error())
			return
		}
		if body.Role != "read" && body.Role != "admin" {
			writeAPIError(response, http.StatusBadRequest, "invalid_role", "Role must be read or admin")
			return
		}
		if body.ProjectID != nil && *body.ProjectID == "" {
			writeAPIError(response, http.StatusBadRequest, "invalid_project", "projectId must be omitted or non-empty")
			return
		}
		timestamp := config.now()
		tokenID, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate API token identifiers")
			return
		}
		value, secret, err := apitoken.Generate(tokenID, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to generate API token secret")
			return
		}
		created, err := config.tokens.CreateAPIToken(request.Context(), state.CreateAPIToken{
			APIToken: state.APIToken{
				ID: tokenID, Name: body.Name, Role: body.Role, ProjectID: body.ProjectID,
				CreatedAtMillis: timestamp.UnixMilli(),
			},
			AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID,
		}, secret)
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to create API token")
			return
		}
		response.Header().Set("Location", "/api/v1/tokens/"+created.ID)
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicAPIToken(created, value))
	}
}

func revokeAPIToken(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate API token revoke identifiers")
			return
		}
		err = config.tokens.RevokeAPIToken(request.Context(), state.RevokeAPIToken{
			ID: request.PathValue("tokenID"), AuditEventID: auditID,
			ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, RevokedAtMillis: timestamp.UnixMilli(),
		})
		if errors.Is(err, state.ErrAPITokenNotFound) {
			writeAPIError(response, http.StatusNotFound, "api_token_not_found", "Active API token not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to revoke API token")
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeAPITokenJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumAPITokenRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid API token fields")
		return false
	}
	return true
}

func publicAPIToken(token state.APIToken, value string) apiTokenResponse {
	return apiTokenResponse{
		ID: token.ID, Name: token.Name, Role: token.Role, ProjectID: token.ProjectID,
		CreatedAt: token.CreatedAtMillis, LastUsedAt: token.LastUsedMillis,
		RevokedAt: token.RevokedAtMillis, Token: value,
	}
}
