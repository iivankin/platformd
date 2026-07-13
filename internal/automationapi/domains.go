package automationapi

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type serviceDomainResponse struct {
	Hostname    string `json:"hostname"`
	ServiceID   string `json:"serviceId"`
	ServiceName string `json:"serviceName,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

func listServiceDomains(application *automation.DomainApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		domains, err := application.List(request.Context(), identity, request.PathValue("projectID"), request.PathValue("serviceID"))
		if writeDomainError(response, err) {
			return
		}
		result := make([]serviceDomainResponse, 0, len(domains))
		for _, domain := range domains {
			result = append(result, publicServiceDomain(domain))
		}
		writeJSON(response, http.StatusOK, map[string]any{"domains": result})
	}
}

func attachServiceDomain(application *automation.DomainApplication) http.HandlerFunc {
	type requestBody struct {
		Hostname string `json:"hostname"`
		Move     bool   `json:"move"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body requestBody
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Attach(request.Context(), identity, automation.AttachDomainInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Hostname: body.Hostname, Move: body.Move,
		})
		if writeDomainError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicServiceDomain(result.Domain))
	}
}

func detachServiceDomain(application *automation.DomainApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		result, err := application.Detach(request.Context(), identity, automation.DetachDomainInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Hostname: request.PathValue("hostname"),
		})
		if writeDomainError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func writeDomainError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var conflict *state.DomainConflict
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, automation.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_domain", err.Error())
	case errors.As(err, &conflict):
		writeJSON(response, http.StatusConflict, map[string]any{
			"error": map[string]any{"code": "domain_conflict", "message": err.Error(), "domain": publicServiceDomain(conflict.Domain)},
		})
	case errors.Is(err, state.ErrServiceNotFound):
		writeError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrDomainNotFound):
		writeError(response, http.StatusNotFound, "domain_not_found", "Domain not found on this service")
	case errors.Is(err, state.ErrServiceTargetPortNeeded):
		writeError(response, http.StatusConflict, "target_port_required", err.Error())
	case errors.Is(err, state.ErrHostnameInUse):
		writeError(response, http.StatusConflict, "hostname_in_use", err.Error())
	case errors.Is(err, state.ErrCertificateCoverage):
		writeError(response, http.StatusUnprocessableEntity, "certificate_not_covered", err.Error())
	default:
		writeError(response, http.StatusBadRequest, "invalid_domain", err.Error())
	}
	return true
}

func publicServiceDomain(domain state.ServiceDomain) serviceDomainResponse {
	return serviceDomainResponse{
		Hostname: domain.Hostname, ServiceID: domain.ServiceID, ServiceName: domain.ServiceName,
		ProjectID: domain.ProjectID, ProjectName: domain.ProjectName, CreatedAt: domain.CreatedAt,
	}
}
