package server

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/cloudflaremesh"
	"github.com/iivankin/platformd/internal/state"
)

func registerCloudflareMeshRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/settings/cloudflare-mesh", getCloudflareMeshSettings(config))
	mux.HandleFunc("GET /api/v1/settings/cloudflare-mesh/credential", getCloudflareMeshCredential(config))
	mux.HandleFunc("PUT /api/v1/settings/cloudflare-mesh", putCloudflareMeshSettings(config))
	mux.HandleFunc("POST /api/v1/settings/cloudflare-mesh/connect", connectCloudflareMesh(config))
}

func publicCloudflareMeshSettings(settings cloudflaremesh.Settings) map[string]any {
	return map[string]any{
		"configured": settings.Configured, "accountId": settings.AccountID,
		"nodeId": settings.NodeID, "nodeName": settings.NodeName,
		"status": settings.Status, "interfaceName": settings.InterfaceName,
		"meshIp": settings.MeshIP, "updatedAt": settings.UpdatedAtMillis,
	}
}

func getCloudflareMeshSettings(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.cloudflareMesh.Settings(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "cloudflare_mesh_settings_failed", "Unable to load Cloudflare Mesh settings")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, publicCloudflareMeshSettings(settings))
	}
}

func getCloudflareMeshCredential(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		credential, err := config.cloudflareMesh.Credential(request.Context())
		if errors.Is(err, state.ErrCloudflareMeshNotConfigured) {
			writeAPIError(response, http.StatusNotFound, "cloudflare_mesh_not_configured", "Cloudflare Mesh is not configured")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "cloudflare_mesh_credential_failed", "Unable to load Cloudflare Mesh credentials")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, map[string]any{"accountId": credential.AccountID, "apiToken": credential.APIToken})
	}
}

func putCloudflareMeshSettings(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		AccountID string `json:"accountId"`
		APIToken  string `json:"apiToken"`
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
			writeAPIError(response, http.StatusInternalServerError, "cloudflare_mesh_configure_failed", "Unable to allocate request IDs")
			return
		}
		settings, err := config.cloudflareMesh.Configure(request.Context(), cloudflaremesh.ConfigureInput{
			AccountID: body.AccountID, APIToken: token, AuditEventID: auditID,
			ActorID: identity.Subject, ActorEmail: identity.Email,
			CorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "cloudflare_mesh_configure_failed", err.Error())
			return
		}
		if config.networkGateways != nil {
			if err := config.networkGateways.ReconcileMeshNetworkGateways(request.Context()); err != nil {
				writeAPIError(response, http.StatusInternalServerError, "cloudflare_mesh_reconcile_failed", err.Error())
				return
			}
		}
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicCloudflareMeshSettings(settings))
	}
}

func connectCloudflareMesh(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.cloudflareMesh.Reconnect(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "cloudflare_mesh_connect_failed", err.Error())
			return
		}
		if config.networkGateways != nil {
			if err := config.networkGateways.ReconcileMeshNetworkGateways(request.Context()); err != nil {
				writeAPIError(response, http.StatusInternalServerError, "cloudflare_mesh_reconcile_failed", err.Error())
				return
			}
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, publicCloudflareMeshSettings(settings))
	}
}
