package automationapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/state"
)

type portForwardRequest struct {
	Port             int `json:"port"`
	LocalPort        int `json:"localPort"`
	ExpiresInSeconds int `json:"expiresInSeconds"`
}

type portForwardResponse struct {
	ID           string                   `json:"id"`
	Ticket       string                   `json:"ticket"`
	ProjectID    string                   `json:"projectId"`
	ResourceKind string                   `json:"resourceKind"`
	ResourceID   string                   `json:"resourceId"`
	Port         int                      `json:"port"`
	ExpiresAt    string                   `json:"expiresAt"`
	Instructions portforward.Instructions `json:"instructions"`
}

func createPortForward(hostname string, application *portforward.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		identity, ok := requireAdminProject(response, request, projectID)
		if !ok {
			return
		}
		var body portForwardRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		localPort := body.LocalPort
		if localPort == 0 {
			localPort = body.Port
		}
		if localPort < 1 || localPort > 65535 {
			writeError(response, http.StatusBadRequest, "invalid_port_forward", "localPort must be from 1 to 65535")
			return
		}
		grant, err := application.Create(request.Context(), identity, portforward.CreateInput{
			ProjectID: projectID, ResourceKind: request.PathValue("kind"),
			ResourceID: request.PathValue("resourceID"), Port: body.Port,
			LifetimeSeconds: body.ExpiresInSeconds,
		})
		if writePortForwardError(response, err) {
			return
		}
		writeJSON(response, http.StatusCreated, portForwardResponse{
			ID: grant.ID, Ticket: grant.Ticket, ProjectID: grant.ProjectID,
			ResourceKind: grant.ResourceKind, ResourceID: grant.ResourceID, Port: grant.Port,
			ExpiresAt:    grant.ExpiresAt.Format(time.RFC3339),
			Instructions: portforward.ConnectionInstructions(hostname, grant.Ticket, localPort),
		})
	}
}

func writePortForwardError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, portforward.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_port_forward", "Port forward input is invalid")
	case errors.Is(err, state.ErrServiceNotFound), errors.Is(err, state.ErrManagedPostgresNotFound), errors.Is(err, state.ErrManagedRedisNotFound):
		writeError(response, http.StatusNotFound, "resource_not_found", "Port forward resource was not found")
	case errors.Is(err, portforward.ErrTargetUnavailable):
		writeError(response, http.StatusConflict, "target_unavailable", "Port forward target is not running")
	case errors.Is(err, portforward.ErrTicketCapacity):
		writeError(response, http.StatusServiceUnavailable, "port_forward_capacity", "Port forward ticket capacity is exhausted")
	default:
		writeError(response, http.StatusInternalServerError, "port_forward_failed", "Unable to create port forward ticket")
	}
	return true
}
