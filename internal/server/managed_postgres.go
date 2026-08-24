package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedstats"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
	"github.com/jackc/pgx/v5/pgconn"
)

const maximumManagedPostgresRequestBytes = 2*managedpostgres.MaximumSQLBytes + 4096

type managedPostgresResponse struct {
	ID                   string                     `json:"id"`
	ProjectID            string                     `json:"projectId"`
	Name                 string                     `json:"name"`
	Hostname             string                     `json:"hostname"`
	Port                 int                        `json:"port"`
	ImageTag             string                     `json:"imageTag"`
	ImageDigest          string                     `json:"imageDigest"`
	DatabaseName         string                     `json:"databaseName"`
	OwnerUsername        string                     `json:"ownerUsername"`
	OwnerPassword        string                     `json:"ownerPassword,omitempty"`
	CPUMillicores        int64                      `json:"cpuMillicores,omitempty"`
	MemoryBytes          int64                      `json:"memoryBytes,omitempty"`
	PortForward          *serviceconfig.PortForward `json:"portForward,omitempty"`
	BackupEnabled        bool                       `json:"backupEnabled"`
	BackupCron           string                     `json:"backupCron,omitempty"`
	BackupRetentionCount int                        `json:"backupRetentionCount"`
	CreatedAt            int64                      `json:"createdAt"`
	UpdatedAt            int64                      `json:"updatedAt"`
}

func registerManagedPostgresRoutes(mux *http.ServeMux, application *managedpostgres.Application, stats *managedstats.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres", listManagedPostgres(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres", createManagedPostgres(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}", getManagedPostgres(application))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/postgres/{postgresID}", deleteManagedPostgres(application))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/postgres/{postgresID}/port-forward", updateManagedPostgresPortForward(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}/extensions", listManagedPostgresExtensions(application))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/postgres/{postgresID}/extensions/{extensionName}", changeManagedPostgresExtension(application, true))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/postgres/{postgresID}/extensions/{extensionName}", changeManagedPostgresExtension(application, false))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres/{postgresID}/query", queryManagedPostgres(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}/stats", getManagedPostgresStats(application))
	if stats != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}/stats/history", getManagedPostgresStatsHistory(application, stats))
	}
	registerManagedDeploymentRoutes(mux, "postgres", application, writeManagedPostgresError)
}

func deleteManagedPostgres(application *managedpostgres.Application) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64 `json:"expectedUpdatedAt"`
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
		result, err := application.Delete(request.Context(), managedpostgres.DeleteInput{
			ProjectID: request.PathValue("projectID"), ResourceID: request.PathValue("postgresID"),
			ExpectedUpdatedAt: body.ExpectedUpdatedAt,
			Actor:             managedpostgres.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func getManagedPostgresStats(application *managedpostgres.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		stats, err := application.Stats(request.Context(), request.PathValue("projectID"), request.PathValue("postgresID"))
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, stats)
	}
}

func getManagedPostgresStatsHistory(application *managedpostgres.Application, stats *managedstats.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if _, err := application.Resource(request.Context(), request.PathValue("projectID"), request.PathValue("postgresID")); err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		history, err := stats.History(
			request.Context(), "postgres", request.PathValue("postgresID"), request.URL.Query().Get("range"),
		)
		if writeManagedStatsError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, history)
	}
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

func updateManagedPostgresPortForward(application *managedpostgres.Application) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64                      `json:"expectedUpdatedAt"`
		PortForward       *serviceconfig.PortForward `json:"portForward"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeManagedPostgresJSON(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_port_forward", "expectedUpdatedAt is required")
			return
		}
		resource, err := application.UpdatePortForward(
			request.Context(),
			request.PathValue("projectID"),
			request.PathValue("postgresID"),
			body.PortForward,
			body.ExpectedUpdatedAt,
		)
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
		Name          string                             `json:"name"`
		ImageTag      string                             `json:"imageTag"`
		CPUMillicores int64                              `json:"cpuMillicores"`
		MemoryBytes   int64                              `json:"memoryBytes"`
		BackupPolicy  initialBackupPolicyRequest         `json:"backupPolicy"`
		Credentials   managedpostgres.InitialCredentials `json:"credentials"`
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
			BackupPolicy: body.BackupPolicy.statePolicy(),
			Credentials:  &body.Credentials,
			Actor:        managedpostgres.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
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

func listManagedPostgresExtensions(application *managedpostgres.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		extensions, err := application.Extensions(
			request.Context(),
			request.PathValue("projectID"),
			request.PathValue("postgresID"),
		)
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"extensions": extensions})
	}
}

func changeManagedPostgresExtension(application *managedpostgres.Application, install bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		result, err := application.ChangeExtension(
			request.Context(),
			managedpostgres.ChangeExtensionInput{
				ProjectID: request.PathValue("projectID"), ResourceID: request.PathValue("postgresID"),
				ExtensionName: request.PathValue("extensionName"), Install: install,
				Actor: managedpostgres.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
			},
		)
		if err != nil {
			writeManagedPostgresError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusAccepted, publicOperation(result.Operation))
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
	return decodeStrictJSONRequest(
		response, request, destination, maximumManagedPostgresRequestBytes,
		"Request body contains invalid managed PostgreSQL fields",
	)
}

func publicManagedPostgres(resource state.ManagedPostgres, password string) managedPostgresResponse {
	return managedPostgresResponse{
		ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		Hostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedpostgres.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		DatabaseName: resource.DatabaseName, OwnerUsername: resource.OwnerUsername, OwnerPassword: password,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		PortForward:   resource.PortForward,
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
	case errors.Is(err, state.ErrManagedPostgresChanged):
		writeAPIError(response, http.StatusConflict, "postgres_changed", "Managed PostgreSQL resource changed")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, state.ErrBackupTargetNotFound), errors.Is(err, state.ErrInvalidBackupPolicy):
		writeAPIError(response, http.StatusBadRequest, "invalid_backup_policy", err.Error())
	case errors.Is(err, managedpostgres.ErrImageUnavailable):
		writeAPIError(response, http.StatusBadGateway, "managed_postgres_image_unavailable", "Unable to resolve the selected official PostgreSQL image", err)
	case errors.Is(err, managedpostgres.ErrInvalidInput), errors.Is(err, managedpostgres.ErrInvalidQuery), errors.Is(err, managedimages.ErrInvalidQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_postgres", err.Error())
	case errors.Is(err, managedpostgres.ErrMaintenance):
		writeAPIError(response, http.StatusConflict, "resource_busy", "Managed PostgreSQL is in maintenance")
	case errors.Is(err, managedpostgres.ErrNotRunning):
		writeAPIError(response, http.StatusServiceUnavailable, "postgres_not_running", "Managed PostgreSQL resource is not running", err)
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(response, http.StatusGatewayTimeout, "postgres_query_timeout", "PostgreSQL query exceeded the execution limit", err)
	case errors.As(err, &postgresError):
		writeAPIError(response, http.StatusUnprocessableEntity, "postgres_query_failed", postgresError.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage PostgreSQL resource", err)
	}
}
