package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type deploymentResponse struct {
	ID             string                 `json:"id"`
	ServiceID      string                 `json:"serviceId"`
	ImageDigest    string                 `json:"imageDigest,omitempty"`
	ImageReference string                 `json:"imageReference,omitempty"`
	SourceRevision string                 `json:"sourceRevision,omitempty"`
	CommitMessage  string                 `json:"commitMessage,omitempty"`
	ConfigHash     string                 `json:"serviceConfigHash"`
	Snapshot       serviceconfig.Snapshot `json:"snapshot"`
	Status         string                 `json:"status"`
	ErrorCode      string                 `json:"errorCode,omitempty"`
	ErrorMessage   string                 `json:"errorMessage,omitempty"`
	CreatedAt      int64                  `json:"createdAt"`
	FinishedAt     int64                  `json:"finishedAt,omitempty"`
}

type deploymentPageResponse struct {
	Deployments []deploymentResponse `json:"deployments"`
	NextCursor  string               `json:"nextCursor,omitempty"`
}

type previewDeploymentResponse struct {
	ID                string `json:"id"`
	ServiceID         string `json:"serviceId"`
	PullRequestNumber int    `json:"pullRequestNumber"`
	SourceRevision    string `json:"sourceRevision"`
	CommitMessage     string `json:"commitMessage,omitempty"`
	Hostname          string `json:"hostname"`
	TargetPort        int    `json:"targetPort"`
	Status            string `json:"status"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	FinishedAt        int64  `json:"finishedAt,omitempty"`
	ExpiresAt         int64  `json:"expiresAt"`
}

func registerServiceLifecycleRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}", getService(config))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/services/{serviceID}", updateService(config))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/services/{serviceID}", deleteService(config))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/redeploy", redeployService(config))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/deployments", listServiceDeployments(config.services))
	if previews, ok := config.services.(PreviewDeploymentRepository); ok {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/previews", listServicePreviews(previews))
	}
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/deployments/{deploymentID}", getServiceDeployment(config.services))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/deployments/{deploymentID}/deploy", deployServiceVersion(config))
	if actions, ok := config.services.(ServiceDeploymentActionRepository); ok {
		mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/deployments/{deploymentID}/restart", restartServiceDeployment(config, actions))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/deployments/{deploymentID}/remove", removeServiceDeployment(config, actions))
	}
}

func listServicePreviews(repository PreviewDeploymentRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		previews, err := repository.ServicePreviewDeployments(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"),
		)
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load PR preview history")
			return
		}
		result := make([]previewDeploymentResponse, 0, len(previews))
		for _, item := range previews {
			result = append(result, previewDeploymentResponse{
				ID: item.ID, ServiceID: item.ServiceID, PullRequestNumber: item.PullRequestNumber,
				SourceRevision: item.SourceRevision, CommitMessage: item.CommitMessage,
				Hostname: item.Hostname, TargetPort: item.TargetPort, Status: item.Status,
				ErrorMessage: item.ErrorMessage, CreatedAt: item.CreatedAtMillis,
				FinishedAt: item.FinishedAtMillis, ExpiresAt: item.ExpiresAtMillis,
			})
		}
		writeJSON(response, http.StatusOK, map[string]any{"previews": result})
	}
}

func deleteService(config handlerConfig) http.HandlerFunc {
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
			writeAPIError(response, http.StatusBadRequest, "invalid_service_delete", "expectedUpdatedAt is required")
			return
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate service deletion identifiers")
			return
		}
		_, err = config.services.DeleteService(request.Context(), state.DeleteServiceInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID:          auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, DeletedAtMillis: timestamp.UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func getService(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		service, err := config.services.Service(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if errors.Is(err, state.ErrServiceNotFound) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load service")
			return
		}
		public, err := publicService(request.Context(), config, service)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to reveal service registry credential")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, public)
	}
}

func updateService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		serviceConfigRequest
		Enabled            *bool                             `json:"enabled"`
		ExpectedUpdatedAt  int64                             `json:"expectedUpdatedAt"`
		RegistryCredential *serviceRegistryCredentialRequest `json:"registryCredential"`
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
		credential, credentialErr := prepareServiceImageCredential(
			request.Context(), config, request.PathValue("serviceID"), snapshot.Source,
			body.RegistryCredential, config.now().UnixMilli(),
		)
		if credentialErr != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_registry_auth", credentialErr.Error())
			return
		}
		updated, err := config.services.UpdateService(request.Context(), state.UpdateServiceInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			Enabled: *body.Enabled, Snapshot: snapshot, ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			ImageCredential: credential, RemoveImageCredential: snapshot.Source.Type != servicesource.PrivateImage,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		public, err := publicService(request.Context(), config, updated)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to reveal service registry credential")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, public)
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
			AuditEventID:          auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writePublicService(response, request, config, service)
	}
}

func deployServiceVersion(config handlerConfig) http.HandlerFunc {
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
			writeAPIError(response, http.StatusBadRequest, "invalid_service_deploy_version", "expectedUpdatedAt is required")
			return
		}
		_, auditID, correlationID, err := createRequestIDs(config.now(), config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate deployment identifiers")
			return
		}
		service, err := config.services.DeployServiceVersion(request.Context(), state.DeployServiceVersionInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"), DeploymentID: request.PathValue("deploymentID"),
			ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID:          auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writePublicService(response, request, config, service)
	}
}

func restartServiceDeployment(config handlerConfig, repository ServiceDeploymentActionRepository) http.HandlerFunc {
	return serviceDeploymentAction(config, repository.RestartServiceDeployment)
}

func removeServiceDeployment(config handlerConfig, repository ServiceDeploymentActionRepository) http.HandlerFunc {
	return serviceDeploymentAction(config, repository.RemoveServiceDeployment)
}

func serviceDeploymentAction(
	config handlerConfig,
	action func(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error),
) http.HandlerFunc {
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
			writeAPIError(response, http.StatusBadRequest, "invalid_service_deployment_action", "expectedUpdatedAt is required")
			return
		}
		_, auditID, correlationID, err := createRequestIDs(config.now(), config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate service action identifiers")
			return
		}
		service, err := action(request.Context(), state.DeleteServiceDeploymentInput{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			DeploymentID: request.PathValue("deploymentID"), ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: config.now().UnixMilli(),
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writePublicService(response, request, config, service)
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

func getServiceDeployment(repository ServiceRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		deployment, err := repository.ServiceDeployment(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"), request.PathValue("deploymentID"),
		)
		switch {
		case err == nil:
			writeJSON(response, http.StatusOK, publicDeployment(deployment))
		case errors.Is(err, state.ErrServiceNotFound):
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
		case errors.Is(err, state.ErrDeploymentNotFound):
			writeAPIError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found for this service")
		default:
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load deployment")
		}
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
		writeAPIError(response, http.StatusConflict, "deployment_not_deployable", "This deployment cannot be deployed again")
	case errors.Is(err, state.ErrDeploymentIsActive):
		writeAPIError(response, http.StatusConflict, "deployment_is_active", "The current deployment must be stopped instead of deleted from history")
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
		ImageDigest: deployment.ImageDigest, ImageReference: deployment.ImageReference,
		SourceRevision: deployment.SourceRevision, CommitMessage: deployment.CommitMessage,
		ConfigHash: deployment.ConfigHash,
		Snapshot:   deployment.Snapshot, Status: deployment.Status,
		ErrorCode: deployment.ErrorCode, ErrorMessage: deployment.ErrorMessage,
		CreatedAt: deployment.CreatedAtMillis, FinishedAt: deployment.FinishedAtMillis,
	}
}
