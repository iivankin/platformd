package server

import (
	"net/http"

	"github.com/iivankin/platformd/internal/access"
)

type identityResponse struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
}

func handleIdentity(response http.ResponseWriter, request *http.Request) {
	identity, ok := access.IdentityFromContext(request.Context())
	if !ok {
		writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
		return
	}
	writeJSON(response, http.StatusOK, identityResponse{Subject: identity.Subject, Email: identity.Email})
}
