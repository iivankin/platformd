package automationapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

const maximumServiceRequestBytes = 300 << 10

type createServiceRequest struct {
	Name          string                 `json:"name"`
	Enabled       *bool                  `json:"enabled"`
	Configuration serviceconfig.Snapshot `json:"configuration"`
}

type updateServiceRequest struct {
	Enabled           *bool                  `json:"enabled"`
	ExpectedUpdatedAt int64                  `json:"expectedUpdatedAt"`
	Configuration     serviceconfig.Snapshot `json:"configuration"`
}

type redeployServiceRequest struct {
	ExpectedUpdatedAt int64 `json:"expectedUpdatedAt"`
}

type rollbackServiceRequest struct {
	DeploymentID      string `json:"deploymentId"`
	ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
}

func createService(application *automation.ServiceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body createServiceRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		result, err := application.Create(request.Context(), identity, automation.CreateServiceInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name,
			Enabled: enabled, Configuration: body.Configuration,
		})
		if writeServiceMutationError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Service.ProjectID+"/services/"+result.Service.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicService(result.Service))
	}
}

func updateService(application *automation.ServiceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body updateServiceRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		if body.Enabled == nil {
			writeError(response, http.StatusBadRequest, "invalid_service_mutation", "enabled is required")
			return
		}
		result, err := application.Update(request.Context(), identity, automation.UpdateServiceInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Enabled: *body.Enabled, ExpectedUpdatedAt: body.ExpectedUpdatedAt, Configuration: body.Configuration,
		})
		writeServiceMutationResult(response, result, err)
	}
}

func redeployService(application *automation.ServiceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body redeployServiceRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Redeploy(request.Context(), identity, automation.RedeployServiceInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			ExpectedUpdatedAt: body.ExpectedUpdatedAt,
		})
		writeServiceMutationResult(response, result, err)
	}
}

func rollbackService(application *automation.ServiceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body rollbackServiceRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Rollback(request.Context(), identity, automation.RollbackServiceInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			DeploymentID: body.DeploymentID, ExpectedUpdatedAt: body.ExpectedUpdatedAt,
		})
		writeServiceMutationResult(response, result, err)
	}
}

func requireAdminProject(response http.ResponseWriter, request *http.Request, projectID string) (automation.Identity, bool) {
	identity, ok := requireIdentity(response, request)
	if !ok {
		return automation.Identity{}, false
	}
	if !identity.IsAdmin() {
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
		return automation.Identity{}, false
	}
	if !identity.AllowsProject(projectID) {
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
		return automation.Identity{}, false
	}
	return identity, true
}

func decodeMutationJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumServiceRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid service fields")
		return false
	}
	return true
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected JSON content")
	}
	return nil
}

func writeServiceMutationResult(response http.ResponseWriter, result automation.ServiceMutationResult, err error) {
	if writeServiceMutationError(response, err) {
		return
	}
	response.Header().Set("X-Request-ID", result.RequestID)
	writeJSON(response, http.StatusOK, publicService(result.Service))
}

func writeServiceMutationError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, automation.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_service_mutation", err.Error())
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrServiceNotFound):
		writeError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrDeploymentNotFound):
		writeError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, state.ErrServiceChanged):
		writeError(response, http.StatusConflict, "service_changed", "Service changed; reload it and retry")
	case errors.Is(err, state.ErrDependencyMissing):
		writeError(response, http.StatusConflict, "dependency_missing", err.Error())
	case errors.Is(err, state.ErrServiceDisabled), errors.Is(err, state.ErrDeploymentNotSuccess):
		writeError(response, http.StatusConflict, "service_state_conflict", err.Error())
	case errors.Is(err, state.ErrImageCredentialNotFound):
		writeError(response, http.StatusNotFound, "image_credential_not_found", "Image credential not found in this project")
	case errors.Is(err, state.ErrImageCredentialHostMismatch):
		writeError(response, http.StatusBadRequest, "image_credential_registry_mismatch", err.Error())
	case errors.Is(err, state.ErrServiceReconcileFailed):
		writeError(response, http.StatusBadGateway, "service_reconcile_failed", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to mutate service")
	}
	return true
}
