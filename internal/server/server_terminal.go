package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/terminalauth"
)

const maximumServerTerminalAuthBytes = 2 << 10

func registerServerTerminalAuthRoute(mux *http.ServeMux, service *terminalauth.Service) {
	type requestBody struct {
		Passphrase string `json:"passphrase"`
	}
	type responseBody struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	mux.HandleFunc("POST /api/v1/server/terminal-token", func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServerTerminalAuthBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil || body.Passphrase == "" {
			writeAPIError(response, http.StatusBadRequest, "invalid_console_passphrase", "A non-empty console passphrase is required")
			return
		}
		passphraseBytes := []byte(body.Passphrase)
		body.Passphrase = ""
		issued, err := service.Issue(request.Context(), identity.Subject, passphraseBytes)
		switch {
		case errors.Is(err, terminalauth.ErrInvalidPassphrase):
			writeAPIError(response, http.StatusUnauthorized, "invalid_console_passphrase", "Console passphrase is invalid")
		case errors.Is(err, terminalauth.ErrCooldown):
			response.Header().Set("Retry-After", "60")
			writeAPIError(response, http.StatusTooManyRequests, "console_passphrase_cooldown", "Console passphrase verification is cooling down")
		case err != nil:
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to authorize server terminal")
		default:
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(response).Encode(responseBody{
				Token: issued.Value, ExpiresAt: issued.ExpiresAt.UnixMilli(),
			})
		}
	})
}
