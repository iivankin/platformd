package server

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerports"
)

func registerContainerPortRoutes(mux *http.ServeMux, application *containerports.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/resources/{resourceKind}/{resourceID}/ports", func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		ports, err := application.List(
			request.Context(), request.PathValue("projectID"), request.PathValue("resourceKind"), request.PathValue("resourceID"),
		)
		if err != nil {
			if errors.Is(err, containerports.ErrResourceNotRunning) {
				writeAPIError(response, http.StatusConflict, "resource_not_running", "The resource has no running container")
				return
			}
			writeAPIError(response, http.StatusConflict, "container_port_probe_failed", "Unable to detect container ports")
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		writeJSON(response, http.StatusOK, struct {
			Ports []containerengine.ListeningPort `json:"ports"`
		}{Ports: ports})
	})
}
