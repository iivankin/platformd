package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/state"
	"github.com/jackc/pgx/v5/pgconn"
)

const maximumManagedPostgresRequestBytes = 2*managedpostgres.MaximumSQLBytes + 4096

type managedPostgresResponse struct {
	ID                   string `json:"id"`
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	Hostname             string `json:"hostname"`
	Port                 int    `json:"port"`
	ImageTag             string `json:"imageTag"`
	ImageDigest          string `json:"imageDigest"`
	DatabaseName         string `json:"databaseName"`
	OwnerUsername        string `json:"ownerUsername"`
	OwnerPassword        string `json:"ownerPassword,omitempty"`
	CPUMillicores        int64  `json:"cpuMillicores,omitempty"`
	MemoryBytes          int64  `json:"memoryBytes,omitempty"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}

func registerManagedPostgresRoutes(mux *http.ServeMux, application *managedpostgres.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres", listManagedPostgres(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres", createManagedPostgres(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}", getManagedPostgres(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres/{postgresID}/query", queryManagedPostgres(application))
	registerManagedDeploymentRoutes(mux, "postgres", application, writeManagedPostgresError)
}

func listManagedPostgres(application *managedpostgres.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resources, err := application.Resources(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		result := make([]managedPostgresResponse, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedPostgres(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedPostgres(application *managedpostgres.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resource, err := application.Resource(request.Context(), request.PathValue("projectID"), request.PathValue("postgresID"))
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		password, err := application.OwnerPassword(request.Context(), request.PathValue("projectID"), request.PathValue("postgresID"))
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedPostgres(resource, password))
	}
}

func createManagedPostgres(application *managedpostgres.Application) http.HandlerFunc {
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
		var body requestBody
		if !decodeManagedPostgresJSON(response, request, &body) {
			return
		}
		result, err := application.Create(request.Context(), managedpostgres.CreateInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
			Actor: managedpostgres.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/postgres/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedPostgres(result.Resource, result.OwnerPassword))
	}
}

func queryManagedPostgres(application *managedpostgres.Application) http.HandlerFunc {
	type requestBody struct {
		SQL string `json:"sql"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeManagedPostgresJSON(response, request, &body) {
			return
		}
		result, err := application.Query(request.Context(), managedpostgres.QueryInput{
			ProjectID: request.PathValue("projectID"), ResourceID: request.PathValue("postgresID"),
			Actor: managedpostgres.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email}, SQL: body.SQL,
		})
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, map[string]any{
			"statements": result.Statements, "truncated": result.Truncated,
			"auditRecorded": result.AuditRecorded,
		})
	}
}

func decodeManagedPostgresJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumManagedPostgresRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid managed PostgreSQL fields")
		return false
	}
	return true
}

func publicManagedPostgres(resource state.ManagedPostgres, password string) managedPostgresResponse {
	return managedPostgresResponse{
		ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		Hostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedpostgres.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		DatabaseName: resource.DatabaseName, OwnerUsername: resource.OwnerUsername, OwnerPassword: password,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount,
		CreatedAt:            resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}

func writeManagedPostgresError(response http.ResponseWriter, err error) {
	var postgresError *pgconn.PgError
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrManagedPostgresNotFound):
		writeAPIError(response, http.StatusNotFound, "postgres_not_found", "Managed PostgreSQL resource not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, managedpostgres.ErrImageUnavailable):
		writeAPIError(response, http.StatusBadGateway, "managed_postgres_image_unavailable", "Unable to resolve the selected official PostgreSQL image")
	case errors.Is(err, managedpostgres.ErrInvalidInput), errors.Is(err, managedpostgres.ErrInvalidQuery), errors.Is(err, managedimages.ErrInvalidQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_postgres", err.Error())
	case errors.Is(err, managedpostgres.ErrMaintenance):
		writeAPIError(response, http.StatusConflict, "resource_busy", "Managed PostgreSQL is in maintenance")
	case errors.Is(err, managedpostgres.ErrNotRunning):
		writeAPIError(response, http.StatusServiceUnavailable, "postgres_not_running", "Managed PostgreSQL resource is not running")
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(response, http.StatusGatewayTimeout, "postgres_query_timeout", "PostgreSQL query exceeded the execution limit")
	case errors.As(err, &postgresError):
		writeAPIError(response, http.StatusUnprocessableEntity, "postgres_query_failed", postgresError.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage PostgreSQL resource")
	}
}
