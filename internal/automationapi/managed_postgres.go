package automationapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/state"
)

type managedPostgresRepository interface {
	ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error)
	ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error)
}

func listManagedPostgres(repository managedPostgresRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resources, err := repository.ManagedPostgresByProject(request.Context(), projectID)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]map[string]any, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedPostgres(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedPostgres(repository managedPostgresRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resource, err := repository.ManagedPostgresInProject(request.Context(), projectID, request.PathValue("postgresID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedPostgres(resource, ""))
	}
}

func createManagedPostgres(application *automation.ManagedPostgresApplication) http.HandlerFunc {
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
		result, err := application.Create(request.Context(), identity, automation.CreateManagedPostgresInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
		})
		if err != nil {
			writeManagedPostgresMutationError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/postgres/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedPostgres(result.Resource, result.OwnerPassword))
	}
}

func publicManagedPostgres(resource state.ManagedPostgres, password string) map[string]any {
	result := map[string]any{
		"id": resource.ID, "projectId": resource.ProjectID, "name": resource.Name,
		"hostname": resource.Name + "." + resource.ProjectName + ".internal", "port": managedpostgres.Port,
		"imageTag": resource.ImageTag, "imageDigest": resource.ImageDigest,
		"databaseName": resource.DatabaseName, "ownerUsername": resource.OwnerUsername,
		"cpuMillicores": resource.CPUMillicores,
		"memoryBytes":   resource.MemoryMaxBytes, "backupEnabled": resource.BackupEnabled,
		"backupCron": resource.BackupCron, "backupRetentionCount": resource.BackupRetentionCount,
		"createdAt": resource.CreatedAtMillis, "updatedAt": resource.UpdatedAtMillis,
	}
	if password != "" {
		result["ownerPassword"] = password
	}
	return result
}

func writeManagedPostgresMutationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, managedpostgres.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_managed_postgres", err.Error())
	case errors.Is(err, managedpostgres.ErrImageUnavailable):
		writeError(response, http.StatusBadGateway, "managed_postgres_image_unavailable", "Unable to resolve the selected official PostgreSQL image")
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to create managed PostgreSQL resource")
	}
}
