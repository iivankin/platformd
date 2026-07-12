package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type deploymentResponse struct {
	ID           string                 `json:"id"`
	ServiceID    string                 `json:"serviceId"`
	ImageDigest  string                 `json:"imageDigest"`
	ConfigHash   string                 `json:"serviceConfigHash"`
	Snapshot     serviceconfig.Snapshot `json:"snapshot"`
	Status       string                 `json:"status"`
	ErrorCode    string                 `json:"errorCode,omitempty"`
	ErrorMessage string                 `json:"errorMessage,omitempty"`
	CreatedAt    int64                  `json:"createdAt"`
	FinishedAt   int64                  `json:"finishedAt,omitempty"`
}

type deploymentPageResponse struct {
	Deployments []deploymentResponse `json:"deployments"`
	NextCursor  string               `json:"nextCursor,omitempty"`
}

func registerServiceLifecycleRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}", getService(config.services))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/services/{serviceID}", updateService(config))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/redeploy", redeployService(config))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/rollback", rollbackService(config))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/deployments", listServiceDeployments(config.services))
}

func getService(repository ServiceRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		service, err := repository.Service(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load service")
			return
		}
		writeJSON(response, http.StatusOK, publicService(service))
	}
}

func updateService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		serviceConfigRequest
		Enabled           *bool `json:"enabled"`
		ExpectedUpdatedAt int64 `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeServiceJSON(response, request, &body) {
			return
		}
		if body.Enabled == nil || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_update", "enabled and expectedUpdatedAt are required")
			return
		}
		snapshot, err := serviceconfig.Normalize(body.snapshot())
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_config", err.Error())
			return
		}
		_, auditID, correlationID, err := createRequestIDs(config.now(), config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate service update identifiers")
			return
		}
		updated, err := config.services.UpdateService(request.Context(), state.UpdateServiceInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			Enabled: *body.Enabled, Snapshot: snapshot, ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusOK, publicService(updated))
	}
}

func redeployService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64 `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeServiceJSON(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_redeploy", "expectedUpdatedAt is required")
			return
		}
		_, auditID, correlationID, err := createRequestIDs(config.now(), config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate redeploy identifiers")
			return
		}
		service, err := config.services.RedeployService(request.Context(), state.RedeployServiceInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID:          auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusOK, publicService(service))
	}
}

func rollbackService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		DeploymentID      string `json:"deploymentId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body requestBody
		if !decodeServiceJSON(response, request, &body) {
			return
		}
		if body.DeploymentID == "" || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_rollback", "deploymentId and expectedUpdatedAt are required")
			return
		}
		_, auditID, correlationID, err := createRequestIDs(config.now(), config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate rollback identifiers")
			return
		}
		service, err := config.services.RollbackService(request.Context(), state.RollbackServiceInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"), DeploymentID: body.DeploymentID,
			ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID:          auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusOK, publicService(service))
	}
}

func listServiceDeployments(repository ServiceRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		limit := 0
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_page_size", "limit must be an integer")
				return
			}
			limit = parsed
		}
		page, err := repository.ServiceDeployments(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"),
			request.URL.Query().Get("cursor"), limit,
		)
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if errors.Is(err, state.ErrDeploymentPageInvalid) || errors.Is(err, state.ErrDeploymentCursorInvalid) {
			writeAPIError(response, http.StatusBadRequest, "invalid_deployment_page", err.Error())
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load deployment history")
			return
		}
		result := deploymentPageResponse{NextCursor: page.NextCursor}
		result.Deployments = make([]deploymentResponse, 0, len(page.Deployments))
		for _, deployment := range page.Deployments {
			result.Deployments = append(result.Deployments, publicDeployment(deployment))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func decodeServiceJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumServiceRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid service fields")
		return false
	}
	return true
}

func writeServiceMutationError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrServiceChanged):
		writeAPIError(response, http.StatusConflict, "service_changed", "Service changed; reload it before applying this action")
	case errors.Is(err, state.ErrDependencyMissing):
		writeAPIError(response, http.StatusConflict, "dependency_missing", err.Error())
	case errors.Is(err, state.ErrDeploymentNotFound):
		writeAPIError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found for this service")
	case errors.Is(err, state.ErrDeploymentNotSuccess):
		writeAPIError(response, http.StatusConflict, "deployment_not_successful", "Only successful deployments can be rolled back")
	case errors.Is(err, state.ErrServiceDisabled):
		writeAPIError(response, http.StatusConflict, "service_disabled", "Disabled service cannot be redeployed")
	case errors.Is(err, state.ErrImageCredentialHostMismatch):
		writeAPIError(response, http.StatusBadRequest, "image_credential_registry_mismatch", err.Error())
	case errors.Is(err, state.ErrServiceReconcileFailed):
		writeAPIError(response, http.StatusBadGateway, "service_reconcile_failed", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "service_action_failed", "Service action failed")
	}
	return true
}

func publicDeployment(deployment state.DeploymentRecord) deploymentResponse {
	return deploymentResponse{
		ID: deployment.ID, ServiceID: deployment.ServiceID,
		ImageDigest: deployment.ImageDigest, ConfigHash: deployment.ConfigHash,
		Snapshot: deployment.Snapshot, Status: deployment.Status,
		ErrorCode: deployment.ErrorCode, ErrorMessage: deployment.ErrorMessage,
		CreatedAt: deployment.CreatedAtMillis, FinishedAt: deployment.FinishedAtMillis,
	}
}
