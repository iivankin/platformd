package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/state"
)

type ServiceListenerRepository interface {
	ServiceListeners(context.Context, string, string) ([]state.ServiceListener, error)
	AttachServiceListener(context.Context, state.AttachServiceListenerInput) (state.ServiceListener, error)
	DetachServiceListener(context.Context, state.DetachServiceListenerInput) error
}

type serviceListenerResponse struct {
	Protocol    string `json:"protocol"`
	PublicPort  int    `json:"publicPort"`
	TargetPort  int    `json:"targetPort"`
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

func registerServiceListenerRoutes(mux *http.ServeMux, config handlerConfig) {
	pattern := "/api/v1/projects/{projectID}/services/{serviceID}/listeners"
	mux.HandleFunc("GET "+pattern, listServiceListeners(config.listeners))
	mux.HandleFunc("POST "+pattern, attachServiceListener(config))
	mux.HandleFunc("DELETE "+pattern+"/{protocol}/{publicPort}", detachServiceListener(config))
}

func listServiceListeners(repository ServiceListenerRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		listeners, err := repository.ServiceListeners(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list service listeners")
			return
		}
		result := make([]serviceListenerResponse, 0, len(listeners))
		for _, listener := range listeners {
			result = append(result, publicServiceListener(listener))
		}
		writeJSON(response, http.StatusOK, map[string]any{"listeners": result})
	}
}

func attachServiceListener(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Protocol   string `json:"protocol"`
		PublicPort int    `json:"publicPort"`
		TargetPort int    `json:"targetPort"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeListenerJSON(response, request, &body) {
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate listener identifiers")
			return
		}
		listener, err := config.listeners.AttachServiceListener(request.Context(), state.AttachServiceListenerInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Protocol: body.Protocol, PublicPort: body.PublicPort, TargetPort: body.TargetPort,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if writeListenerMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicServiceListener(listener))
	}
}

func detachServiceListener(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		publicPort, err := strconv.Atoi(request.PathValue("publicPort"))
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_listener", "Public port must be an integer")
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate listener identifiers")
			return
		}
		err = config.listeners.DetachServiceListener(request.Context(), state.DetachServiceListenerInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Protocol: request.PathValue("protocol"), PublicPort: publicPort,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if writeListenerMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeListenerJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid listener fields")
		return false
	}
	return true
}

func writeListenerMutationError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var conflict *state.ListenerConflict
	switch {
	case errors.As(err, &conflict):
		writeJSON(response, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code": "listener_conflict", "message": err.Error(),
				"listener": publicServiceListener(conflict.Listener),
			},
		})
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrServiceListenerNotFound):
		writeAPIError(response, http.StatusNotFound, "listener_not_found", "Listener not found on this service")
	case errors.Is(err, state.ErrPublicPortReserved):
		writeAPIError(response, http.StatusConflict, "public_port_reserved", err.Error())
	case errors.Is(err, state.ErrPublicPortUnavailable):
		writeAPIError(response, http.StatusConflict, "public_port_unavailable", err.Error())
	default:
		writeAPIError(response, http.StatusBadRequest, "invalid_listener", err.Error())
	}
	return true
}

func publicServiceListener(listener state.ServiceListener) serviceListenerResponse {
	return serviceListenerResponse{
		Protocol: listener.Protocol, PublicPort: listener.PublicPort, TargetPort: listener.TargetPort,
		ServiceID: listener.ServiceID, ServiceName: listener.ServiceName,
		ProjectID: listener.ProjectID, ProjectName: listener.ProjectName, CreatedAt: listener.CreatedAt,
	}
}
