package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

const maximumServiceRequestBytes = 300 << 10

type ServiceRepository interface {
	CreateService(context.Context, state.CreateService) (state.ServiceDesired, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error)
	UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error)
	RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error)
	RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error)
}

type serviceResponse struct {
	ID                 string                          `json:"id"`
	ProjectID          string                          `json:"projectId"`
	Name               string                          `json:"name"`
	ImageReference     string                          `json:"imageReference"`
	ImageCredentialID  string                          `json:"imageCredentialId,omitempty"`
	Command            []string                        `json:"command,omitempty"`
	Args               []string                        `json:"args,omitempty"`
	Environment        map[string]string               `json:"environment"`
	TargetPort         *int                            `json:"targetPort,omitempty"`
	HealthPath         string                          `json:"healthPath,omitempty"`
	StartupTimeout     int                             `json:"startupTimeoutSeconds"`
	CPUMillicores      int64                           `json:"cpuMillicores,omitempty"`
	MemoryMaxBytes     int64                           `json:"memoryMaxBytes,omitempty"`
	Enabled            bool                            `json:"enabled"`
	ActiveDeploymentID string                          `json:"activeDeploymentId,omitempty"`
	ActiveImageDigest  string                          `json:"activeImageDigest,omitempty"`
	ActiveConfigHash   string                          `json:"activeConfigHash,omitempty"`
	SecretReferences   []serviceconfig.SecretReference `json:"secretReferences"`
	VolumeMounts       []serviceconfig.VolumeMount     `json:"volumeMounts"`
	CreatedAt          int64                           `json:"createdAt"`
	UpdatedAt          int64                           `json:"updatedAt"`
}

type serviceConfigRequest struct {
	ImageReference        string                          `json:"imageReference"`
	ImageCredentialID     string                          `json:"imageCredentialId"`
	Command               []string                        `json:"command"`
	Args                  []string                        `json:"args"`
	Environment           map[string]string               `json:"environment"`
	SecretReferences      []serviceconfig.SecretReference `json:"secretReferences"`
	TargetPort            *int                            `json:"targetPort"`
	HealthPath            string                          `json:"healthPath"`
	StartupTimeoutSeconds int                             `json:"startupTimeoutSeconds"`
	CPUMillicores         int64                           `json:"cpuMillicores"`
	MemoryMaxBytes        int64                           `json:"memoryMaxBytes"`
	VolumeMounts          []serviceconfig.VolumeMount     `json:"volumeMounts"`
}

func (request serviceConfigRequest) snapshot() serviceconfig.Snapshot {
	return serviceconfig.Snapshot{
		ImageReference: request.ImageReference, ImageCredentialID: request.ImageCredentialID,
		Command: request.Command, Args: request.Args, Environment: request.Environment,
		SecretReferences: request.SecretReferences,
		TargetPort:       request.TargetPort, HealthPath: request.HealthPath,
		StartupTimeoutSeconds: request.StartupTimeoutSeconds,
		CPUMillicores:         request.CPUMillicores, MemoryMaxBytes: request.MemoryMaxBytes,
		VolumeMounts: request.VolumeMounts,
	}
}

func registerServiceRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services", createService(config))
	registerServiceLifecycleRoutes(mux, config)
}

func createService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		serviceConfigRequest
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServiceRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only service fields")
			return
		}
		if err := resourcename.Validate(body.Name); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_name", err.Error())
			return
		}
		snapshot, err := serviceconfig.Normalize(body.snapshot())
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_config", err.Error())
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		timestamp := config.now()
		serviceID, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate service identifiers")
			return
		}
		created, err := config.services.CreateService(request.Context(), state.CreateService{
			ID: serviceID, ProjectID: request.PathValue("projectID"), Name: body.Name,
			Enabled: enabled, Snapshot: snapshot,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if errors.Is(err, state.ErrResourceNameConflict) {
			writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
			return
		}
		if errors.Is(err, state.ErrImageCredentialNotFound) {
			writeAPIError(response, http.StatusNotFound, "image_credential_not_found", "Image credential not found in this project")
			return
		}
		if errors.Is(err, state.ErrImageCredentialHostMismatch) {
			writeAPIError(response, http.StatusBadRequest, "image_credential_registry_mismatch", err.Error())
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to create service")
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ProjectID+"/services/"+created.ID)
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicService(created))
	}
}

func publicService(service state.ServiceDesired) serviceResponse {
	return serviceResponse{
		ID: service.ID, ProjectID: service.ProjectID, Name: service.Name,
		ImageReference:    service.Snapshot.ImageReference,
		ImageCredentialID: service.Snapshot.ImageCredentialID,
		Command:           service.Snapshot.Command, Args: service.Snapshot.Args,
		Environment: service.Snapshot.Environment, TargetPort: service.Snapshot.TargetPort,
		HealthPath:     service.Snapshot.HealthPath,
		StartupTimeout: service.Snapshot.StartupTimeoutSeconds,
		CPUMillicores:  service.Snapshot.CPUMillicores,
		MemoryMaxBytes: service.Snapshot.MemoryMaxBytes,
		Enabled:        service.Enabled, ActiveDeploymentID: service.ActiveDeploymentID,
		ActiveImageDigest: service.ActiveImageDigest, ActiveConfigHash: service.ActiveConfigHash,
		SecretReferences: service.Snapshot.SecretReferences, VolumeMounts: service.Snapshot.VolumeMounts,
		CreatedAt: service.CreatedAtMillis, UpdatedAt: service.UpdatedAtMillis,
	}
}
