package automationapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/automation"
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
	Project      string                   `json:"project"`
	Resource     string                   `json:"resource"`
	ResourceKind string                   `json:"resourceKind"`
	Port         int                      `json:"port"`
	ExpiresAt    string                   `json:"expiresAt"`
	Instructions portforward.Instructions `json:"instructions"`
}

func createPortForward(hostname string, application *portforward.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		if !identity.IsAdmin() {
			writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
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
			Project: request.PathValue("projectName"), Resource: request.PathValue("resourceName"), Port: body.Port,
			LifetimeSeconds: body.ExpiresInSeconds,
		})
		if writePortForwardError(response, err) {
			return
		}
		writeJSON(response, http.StatusCreated, portForwardResponse{
			ID: grant.ID, Ticket: grant.Ticket, Project: grant.Project, Resource: grant.Resource,
			ResourceKind: grant.ResourceKind, Port: grant.Port,
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
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, portforward.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_port_forward", "Port forward input is invalid")
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project was not found")
	case errors.Is(err, state.ErrProjectResourceNotFound):
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
