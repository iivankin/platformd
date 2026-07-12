package automationapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

type managedRedisRepository interface {
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error)
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

func listManagedRedis(repository managedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resources, err := repository.ManagedRedisByProject(request.Context(), projectID)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]managedRedisResponse, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedRedis(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedRedis(repository managedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resource, err := repository.ManagedRedisInProject(request.Context(), projectID, request.PathValue("redisID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedis(resource, ""))
	}
}

func createManagedRedis(application *automation.ManagedRedisApplication) http.HandlerFunc {
	type requestBody struct {
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
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
		result, err := application.Create(request.Context(), identity, automation.CreateManagedRedisInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
		})
		if err != nil {
			writeManagedRedisMutationError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/redis/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedRedis(result.Resource, result.Password))
	}
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

func writeManagedRedisMutationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, managedredis.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_managed_redis", err.Error())
	case errors.Is(err, managedredis.ErrImageUnavailable):
		writeError(response, http.StatusBadGateway, "managed_redis_image_unavailable", "Unable to resolve the selected official Redis image")
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to create managed Redis resource")
	}
}
