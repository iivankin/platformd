package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

const maximumManagedRedisRequestBytes = 16 << 10

type ManagedRedisRepository interface {
	Create(context.Context, managedredis.CreateInput) (managedredis.CreateResult, error)
	Resource(context.Context, string, string) (state.ManagedRedis, error)
	Resources(context.Context, string) ([]state.ManagedRedis, error)
}

type managedRedisResponse struct {
	ID                   string `json:"id"`
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	Hostname             string `json:"hostname"`
	Port                 int    `json:"port"`
	ImageTag             string `json:"imageTag"`
	ImageDigest          string `json:"imageDigest"`
	CPUMillicores        int64  `json:"cpuMillicores,omitempty"`
	MemoryBytes          int64  `json:"memoryBytes,omitempty"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	Password             string `json:"password,omitempty"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}

func registerManagedRedisRoutes(mux *http.ServeMux, repository ManagedRedisRepository) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis", listManagedRedis(repository))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis", createManagedRedis(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}", getManagedRedis(repository))
}

func listManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resources, err := repository.Resources(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		result := make([]managedRedisResponse, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedRedis(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resource, err := repository.Resource(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedis(resource, ""))
	}
}

func createManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	type requestBody struct {
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumManagedRedisRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only managed Redis fields")
			return
		}
		result, err := repository.Create(request.Context(), managedredis.CreateInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
			Actor: managedredis.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/redis/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedRedis(result.Resource, result.Password))
	}
}

func requireAccessIdentity(response http.ResponseWriter, request *http.Request) (access.Identity, bool) {
	identity, ok := access.IdentityFromContext(request.Context())
	if !ok {
		writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
	}
	return identity, ok
}

func publicManagedRedis(resource state.ManagedRedis, password string) managedRedisResponse {
	return managedRedisResponse{
		ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		Hostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedredis.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount, Password: password,
		CreatedAt: resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}

func writeManagedRedisError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrManagedRedisNotFound):
		writeAPIError(response, http.StatusNotFound, "redis_not_found", "Managed Redis resource not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, managedredis.ErrImageUnavailable):
		writeAPIError(response, http.StatusBadGateway, "managed_redis_image_unavailable", "Unable to resolve the selected official Redis image")
	case errors.Is(err, managedredis.ErrInvalidInput), errors.Is(err, managedimages.ErrInvalidQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_redis", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage Redis resource")
	}
}
