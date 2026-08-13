package automationapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/state"
)

type portForwardRequest struct {
	Endpoint         string `json:"endpoint"`
	Port             int    `json:"port"`
	LocalPort        int    `json:"localPort"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

type portForwardResponse struct {
	ID           string                   `json:"id"`
	Ticket       string                   `json:"ticket"`
	Project      string                   `json:"project"`
	Resource     string                   `json:"resource"`
	ResourceKind string                   `json:"resourceKind"`
	Endpoint     string                   `json:"endpoint,omitempty"`
	EndpointHost string                   `json:"endpointHost,omitempty"`
	Port         int                      `json:"port"`
	ExpiresAt    string                   `json:"expiresAt"`
	Instructions portforward.Instructions `json:"instructions"`
}

type PortForwardCreateConfig struct {
	Hostname      string
	Application   *portforward.Application
	Authenticator *automationauth.Authenticator
}

func CreatePortForwardHandler(config PortForwardCreateConfig) (http.Handler, error) {
	if config.Hostname == "" || config.Application == nil || config.Authenticator == nil {
		return nil, errors.New("port forward create handler dependencies are incomplete")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		if request.Method != http.MethodPost {
			writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		token, err := bearerToken(request)
		if err != nil {
			writePortForwardUnauthorized(response)
			return
		}
		var body portForwardRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		localPort := body.LocalPort
		if localPort == 0 {
			if body.Endpoint == portforward.EndpointErrors {
				localPort = firewall.ServiceTelemetryPort
			} else {
				localPort = body.Port
			}
		}
		if localPort < 1 || localPort > 65535 {
			writeError(response, http.StatusBadRequest, "invalid_port_forward", "localPort must be from 1 to 65535")
			return
		}
		input := portforward.CreateInput{
			Project:         request.PathValue("projectName"),
			Resource:        request.PathValue("resourceName"),
			Endpoint:        body.Endpoint,
			Port:            body.Port,
			LifetimeSeconds: body.ExpiresInSeconds,
		}

		var grant portforward.Grant
		if strings.HasPrefix(token, "ptk_") {
			identity, retryAfter, authErr := config.Authenticator.Authenticate(request)
			if retryAfter > 0 {
				response.Header().Set("Retry-After", "1")
				http.Error(response, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			if authErr != nil {
				writePortForwardUnauthorized(response)
				return
			}
			if !identity.IsAdmin() {
				writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
				return
			}
			grant, err = config.Application.Create(request.Context(), identity, input)
		} else {
			audience := "https://" + config.Hostname + request.URL.Path
			grant, err = config.Application.CreateOIDC(request.Context(), input, token, audience)
		}
		if writePortForwardError(response, err) {
			return
		}
		writeJSON(response, http.StatusCreated, portForwardResponse{
			ID: grant.ID, Ticket: grant.Ticket, Project: grant.Project, Resource: grant.Resource,
			ResourceKind: grant.ResourceKind, Endpoint: grant.Endpoint,
			EndpointHost: grant.EndpointHost, Port: grant.Port,
			ExpiresAt:    grant.ExpiresAt.Format(time.RFC3339),
			Instructions: portforward.ConnectionInstructions(config.Hostname, grant.Ticket, localPort, grant.EndpointHost),
		})
	}), nil
}

func bearerToken(request *http.Request) (string, error) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", errors.New("authorization required")
	}
	scheme, value, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("authorization required")
	}
	return value, nil
}

func writePortForwardUnauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="platformd automation"`)
	writeError(response, http.StatusUnauthorized, "unauthorized", "Authentication required")
}

func writePortForwardError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, portforward.ErrOIDCUnauthorized):
		writePortForwardUnauthorized(response)
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
