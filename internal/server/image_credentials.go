package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

const maximumImageCredentialRequestBytes = 70 << 10

type CreateImageCredential struct {
	ID                   string
	ProjectID            string
	Name                 string
	RegistryHost         string
	Username             string
	Password             string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	CreatedAtMillis      int64
}

type ImageCredentialRepository interface {
	ImageCredentials(context.Context, string) ([]state.ImageRegistryCredential, error)
	CreateImageCredential(context.Context, CreateImageCredential) (state.ImageRegistryCredential, error)
}

type imageCredentialResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RegistryHost string `json:"registryHost"`
	Username     string `json:"username"`
	CreatedAt    int64  `json:"createdAt"`
}

func registerImageCredentialRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/image-credentials", listImageCredentials(config.imageCredentials))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/image-credentials", createImageCredential(config))
}

func listImageCredentials(repository ImageCredentialRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		credentials, err := repository.ImageCredentials(request.Context(), request.PathValue("projectID"))
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list image credentials")
			return
		}
		result := make([]imageCredentialResponse, 0, len(credentials))
		for _, credential := range credentials {
			result = append(result, publicImageCredential(credential))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func createImageCredential(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Name         string `json:"name"`
		RegistryHost string `json:"registryHost"`
		Username     string `json:"username"`
		Password     string `json:"password"`
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
		request.Body = http.MaxBytesReader(response, request.Body, maximumImageCredentialRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only image credential fields")
			return
		}
		if err := resourcename.Validate(body.Name); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_name", err.Error())
			return
		}
		host, err := imagecredential.NormalizeHost(body.RegistryHost)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_registry_host", err.Error())
			return
		}
		if err := imagecredential.ValidateAuthentication(body.Username, body.Password); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_registry_auth", err.Error())
			return
		}
		timestamp := config.now()
		credentialID, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate credential identifiers")
			return
		}
		created, err := config.imageCredentials.CreateImageCredential(request.Context(), CreateImageCredential{
			ID: credentialID, ProjectID: request.PathValue("projectID"), Name: body.Name,
			RegistryHost: host, Username: body.Username, Password: body.Password,
			AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if errors.Is(err, state.ErrImageCredentialNameConflict) {
			writeAPIError(response, http.StatusConflict, "image_credential_name_conflict", "An image credential with this name already exists")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to create image credential")
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ProjectID+"/image-credentials/"+created.ID)
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicImageCredential(created))
	}
}

func publicImageCredential(credential state.ImageRegistryCredential) imageCredentialResponse {
	return imageCredentialResponse{
		ID: credential.ID, Name: credential.Name, RegistryHost: credential.RegistryHost,
		Username: credential.Username, CreatedAt: credential.CreatedAtMillis,
	}
}
