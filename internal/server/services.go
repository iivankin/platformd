package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

const maximumServiceRequestBytes = 300 << 10

type ServiceRepository interface {
	CreateService(context.Context, state.CreateService) (state.ServiceDesired, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ServiceDeployment(context.Context, string, string, string) (state.DeploymentRecord, error)
	ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error)
	UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error)
	DeleteService(context.Context, state.DeleteServiceInput) (state.DeleteServiceResult, error)
	DeployServiceVersion(context.Context, state.DeployServiceVersionInput) (state.ServiceDesired, error)
	RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error)
}

type PreviewDeploymentRepository interface {
	ServicePreviewDeployments(context.Context, string, string) ([]state.PreviewDeployment, error)
}

type ServiceEnvironmentResolver interface {
	Resolve(context.Context, state.ServiceDesired, string) (map[string]string, error)
}

type ServiceDeploymentActionRepository interface {
	RestartServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error)
	RemoveServiceDeployment(context.Context, state.DeleteServiceDeploymentInput) (state.ServiceDesired, error)
}

type serviceResponse struct {
	ID                 string                             `json:"id"`
	ProjectID          string                             `json:"projectId"`
	Name               string                             `json:"name"`
	Source             servicesource.Source               `json:"source"`
	Command            []string                           `json:"command,omitempty"`
	Args               []string                           `json:"args,omitempty"`
	Environment        map[string]string                  `json:"environment"`
	HealthCheck        *serviceconfig.HealthCheck         `json:"healthCheck,omitempty"`
	CPUMillicores      int64                              `json:"cpuMillicores,omitempty"`
	MemoryMaxBytes     int64                              `json:"memoryMaxBytes,omitempty"`
	Enabled            bool                               `json:"enabled"`
	ActiveDeploymentID string                             `json:"activeDeploymentId,omitempty"`
	ActiveImageDigest  string                             `json:"activeImageDigest,omitempty"`
	ActiveConfigHash   string                             `json:"activeConfigHash,omitempty"`
	SecretReferences   []serviceconfig.SecretReference    `json:"secretReferences"`
	VolumeMounts       []serviceconfig.VolumeMount        `json:"volumeMounts"`
	CreatedAt          int64                              `json:"createdAt"`
	UpdatedAt          int64                              `json:"updatedAt"`
	RegistryCredential *serviceRegistryCredentialResponse `json:"registryCredential,omitempty"`
}

type serviceRegistryCredentialResponse struct {
	RegistryHost string `json:"registryHost"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type serviceRegistryCredentialRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type serviceConfigRequest struct {
	Source           servicesource.Source            `json:"source"`
	Command          []string                        `json:"command"`
	Args             []string                        `json:"args"`
	Environment      map[string]string               `json:"environment"`
	SecretReferences []serviceconfig.SecretReference `json:"secretReferences"`
	HealthCheck      *serviceconfig.HealthCheck      `json:"healthCheck"`
	CPUMillicores    int64                           `json:"cpuMillicores"`
	MemoryMaxBytes   int64                           `json:"memoryMaxBytes"`
	VolumeMounts     []serviceconfig.VolumeMount     `json:"volumeMounts"`
}

func (request serviceConfigRequest) snapshot() serviceconfig.Snapshot {
	return serviceconfig.Snapshot{
		Source:  request.Source,
		Command: request.Command, Args: request.Args, Environment: request.Environment,
		SecretReferences: request.SecretReferences, HealthCheck: request.HealthCheck,
		CPUMillicores: request.CPUMillicores, MemoryMaxBytes: request.MemoryMaxBytes,
		VolumeMounts: request.VolumeMounts,
	}
}

func registerServiceRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services", createService(config))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/variables/resolved", resolvedServiceVariables(config))
	registerServiceLifecycleRoutes(mux, config)
}

func createService(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		serviceConfigRequest
		Name               string                            `json:"name"`
		Enabled            *bool                             `json:"enabled"`
		RegistryCredential *serviceRegistryCredentialRequest `json:"registryCredential"`
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
		credential, credentialErr := prepareServiceImageCredential(
			request.Context(), config, serviceID, snapshot.Source,
			body.RegistryCredential, timestamp.UnixMilli(),
		)
		if credentialErr != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_registry_auth", credentialErr.Error())
			return
		}
		created, err := config.services.CreateService(request.Context(), state.CreateService{
			ID: serviceID, ProjectID: request.PathValue("projectID"), Name: body.Name,
			Enabled: enabled, Snapshot: snapshot, ImageCredential: credential,
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
		public, err := publicService(request.Context(), config, created)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to reveal service registry credential")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusCreated, public)
	}
}

func publicService(ctx context.Context, config handlerConfig, service state.ServiceDesired) (serviceResponse, error) {
	result := serviceResponse{
		ID: service.ID, ProjectID: service.ProjectID, Name: service.Name,
		Source:  service.Snapshot.Source,
		Command: service.Snapshot.Command, Args: service.Snapshot.Args,
		Environment: service.Snapshot.Environment, HealthCheck: service.Snapshot.HealthCheck,
		CPUMillicores:  service.Snapshot.CPUMillicores,
		MemoryMaxBytes: service.Snapshot.MemoryMaxBytes,
		Enabled:        service.Enabled, ActiveDeploymentID: service.ActiveDeploymentID,
		ActiveImageDigest: service.ActiveImageDigest, ActiveConfigHash: service.ActiveConfigHash,
		SecretReferences: service.Snapshot.SecretReferences,
		VolumeMounts:     service.Snapshot.VolumeMounts,
		CreatedAt:        service.CreatedAtMillis, UpdatedAt: service.UpdatedAtMillis,
	}
	if service.Snapshot.Source.Type == servicesource.PrivateImage {
		if config.serviceImageCredentials == nil {
			return serviceResponse{}, errors.New("service image credentials are not configured")
		}
		host, username, password, err := config.serviceImageCredentials.RevealServiceImageCredential(ctx, service.ID)
		if err != nil {
			return serviceResponse{}, err
		}
		result.RegistryCredential = &serviceRegistryCredentialResponse{
			RegistryHost: host, Username: username, Password: password,
		}
	}
	return result, nil
}

func writePublicService(response http.ResponseWriter, request *http.Request, config handlerConfig, service state.ServiceDesired) bool {
	public, err := publicService(request.Context(), config, service)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to reveal service registry credential")
		return false
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, public)
	return true
}

func prepareServiceImageCredential(
	ctx context.Context,
	config handlerConfig,
	serviceID string,
	source servicesource.Source,
	request *serviceRegistryCredentialRequest,
	updatedAt int64,
) (*state.ServiceImageCredential, error) {
	if source.Type != servicesource.PrivateImage {
		if request != nil {
			return nil, errors.New("registry credentials are only valid for private image sources")
		}
		return nil, nil
	}
	if config.serviceImageCredentials == nil {
		return nil, errors.New("service image credentials are not configured")
	}
	input := ServiceImageCredentialInput{
		ServiceID: serviceID, ImageReference: servicesource.ImageReference(source), UpdatedAtMillis: updatedAt,
	}
	if request != nil {
		input.Username = request.Username
		input.Password = request.Password
	}
	return config.serviceImageCredentials.PrepareServiceImageCredential(ctx, input)
}

func resolvedServiceVariables(config handlerConfig) http.HandlerFunc {
	type responseBody struct {
		Environment map[string]string `json:"environment"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if config.serviceEnvironment == nil {
			writeAPIError(response, http.StatusServiceUnavailable, "variable_resolution_unavailable", "Variable resolution is unavailable")
			return
		}
		service, err := config.services.Service(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if errors.Is(err, state.ErrServiceNotFound) || errors.Is(err, sql.ErrNoRows) {
			writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load service")
			return
		}
		environment, err := config.serviceEnvironment.Resolve(request.Context(), service, service.ActiveDeploymentID)
		if err != nil {
			writeAPIError(response, http.StatusUnprocessableEntity, "variable_resolution_failed", err.Error())
			return
		}
		writeJSON(response, http.StatusOK, responseBody{Environment: environment})
	}
}
