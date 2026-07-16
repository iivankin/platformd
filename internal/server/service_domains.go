package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/domainvariables"
	"github.com/iivankin/platformd/internal/state"
)

type DomainRepository interface {
	ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error)
	AttachServiceDomain(context.Context, state.AttachServiceDomainInput) (state.ServiceDomain, error)
	DetachServiceDomain(context.Context, state.DetachServiceDomainInput) error
}

type serviceDomainResponse struct {
	Hostname           string `json:"hostname"`
	ServiceID          string `json:"serviceId"`
	ServiceName        string `json:"serviceName,omitempty"`
	ProjectID          string `json:"projectId,omitempty"`
	ProjectName        string `json:"projectName,omitempty"`
	TargetPort         int    `json:"targetPort"`
	PublicOutputName   string `json:"publicOutputName"`
	InternalOutputName string `json:"internalOutputName"`
	CreatedAt          int64  `json:"createdAt"`
}

func registerServiceDomainRoutes(mux *http.ServeMux, config handlerConfig) {
	pattern := "/api/v1/projects/{projectID}/services/{serviceID}/domains"
	mux.HandleFunc("GET "+pattern, listServiceDomains(config.domains))
	mux.HandleFunc("POST "+pattern, attachServiceDomain(config))
	mux.HandleFunc("DELETE "+pattern+"/{hostname}", detachServiceDomain(config))
}

func listServiceDomains(repository DomainRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		domains, err := repository.ServiceDomains(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list service domains")
			return
		}
		result := make([]serviceDomainResponse, 0, len(domains))
		for _, domain := range domains {
			result = append(result, publicServiceDomain(domain))
		}
		writeJSON(response, http.StatusOK, map[string]any{"domains": result})
	}
}

func attachServiceDomain(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Hostname   string `json:"hostname"`
		TargetPort int    `json:"targetPort"`
		Move       bool   `json:"move"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeDomainJSON(response, request, &body) {
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate domain identifiers")
			return
		}
		domain, err := config.domains.AttachServiceDomain(request.Context(), state.AttachServiceDomainInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Hostname: body.Hostname, TargetPort: body.TargetPort, Move: body.Move,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if writeDomainMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicServiceDomain(domain))
	}
}

func detachServiceDomain(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate domain identifiers")
			return
		}
		err = config.domains.DetachServiceDomain(request.Context(), state.DetachServiceDomainInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"), Hostname: request.PathValue("hostname"),
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if writeDomainMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeDomainJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid domain fields")
		return false
	}
	return true
}

func writeDomainMutationError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var conflict *state.DomainConflict
	switch {
	case errors.As(err, &conflict):
		writeJSON(response, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code": "domain_conflict", "message": err.Error(), "domain": publicServiceDomain(conflict.Domain),
			},
		})
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrDomainNotFound):
		writeAPIError(response, http.StatusNotFound, "domain_not_found", "Domain not found on this service")
	case errors.Is(err, state.ErrHostnameInUse):
		writeAPIError(response, http.StatusConflict, "hostname_in_use", err.Error())
	case errors.Is(err, state.ErrCertificateCoverage):
		writeAPIError(response, http.StatusUnprocessableEntity, "certificate_not_covered", err.Error())
	default:
		writeAPIError(response, http.StatusBadRequest, "invalid_domain", err.Error())
	}
	return true
}

func publicServiceDomain(domain state.ServiceDomain) serviceDomainResponse {
	names, _ := domainvariables.OutputNames(domain.Hostname)
	return serviceDomainResponse{
		Hostname: domain.Hostname, ServiceID: domain.ServiceID, ServiceName: domain.ServiceName,
		ProjectID: domain.ProjectID, ProjectName: domain.ProjectName, TargetPort: domain.TargetPort,
		PublicOutputName: names.Public, InternalOutputName: names.Internal, CreatedAt: domain.CreatedAt,
	}
}
