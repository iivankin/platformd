package server

import (
	"net/http"

	"github.com/iivankin/platformd/internal/cloudflaredns"
)

func registerCloudflareDNSRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/settings/cloudflare", getCloudflareDNSSettings(config))
	mux.HandleFunc("PUT /api/v1/settings/cloudflare", putCloudflareDNSSettings(config))
}

func publicCloudflareDNSSettings(settings cloudflaredns.Settings) map[string]any {
	return map[string]any{
		"configured": settings.Configured,
		"updatedAt":  settings.UpdatedAtMillis,
	}
}

func getCloudflareDNSSettings(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.cloudflareDNS.Settings(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "cloudflare_dns_settings_failed", "Unable to load Cloudflare DNS settings")
			return
		}
		writeJSON(response, http.StatusOK, publicCloudflareDNSSettings(settings))
	}
}

func putCloudflareDNSSettings(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		APIToken string `json:"apiToken"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeInstallationSettingsJSON(response, request, &body) {
			return
		}
		token := []byte(body.APIToken)
		body.APIToken = ""
		defer clear(token)
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "cloudflare_dns_configure_failed", "Unable to allocate request IDs")
			return
		}
		settings, err := config.cloudflareDNS.Configure(request.Context(), cloudflaredns.ConfigureInput{
			APIToken: token, AuditEventID: auditID,
			ActorID: identity.Subject, ActorEmail: identity.Email,
			CorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "cloudflare_dns_configure_failed", err.Error())
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicCloudflareDNSSettings(settings))
	}
}
