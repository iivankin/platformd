package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/state"
)

const maximumDatabaseVersionRequestBytes = 16 << 10

func registerDatabaseVersionRoutes(mux *http.ServeMux, service *databaseversion.Service) {
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis/{redisID}/version-change", startDatabaseVersionChange(service, databaseversion.Redis, "redisID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/version-change/{operationID}", readDatabaseVersionChange(service, databaseversion.Redis, "redisID"))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres/{postgresID}/version-change", startDatabaseVersionChange(service, databaseversion.Postgres, "postgresID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}/version-change/{operationID}", readDatabaseVersionChange(service, databaseversion.Postgres, "postgresID"))
}

func startDatabaseVersionChange(service *databaseversion.Service, kind, resourcePathValue string) http.HandlerFunc {
	type requestBody struct {
		ImageTag string `json:"imageTag"`
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
		request.Body = http.MaxBytesReader(response, request.Body, maximumDatabaseVersionRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_database_version_change", "Request body must contain only imageTag")
			return
		}
		result, err := service.Start(
			request.Context(), kind, request.PathValue("projectID"), request.PathValue(resourcePathValue),
			body.ImageTag, databaseversion.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		)
		if writeDatabaseVersionError(response, err) {
			return
		}
		response.Header().Set("Location", request.URL.Path+"/"+result.Operation.ID)
		writeJSON(response, http.StatusAccepted, map[string]any{
			"operation": publicOperation(result.Operation),
			"sourceTag": result.SourceTag, "sourceDigest": result.SourceDigest,
			"targetTag": result.TargetTag, "targetDigest": result.TargetDigest,
		})
	}
}

func readDatabaseVersionChange(service *databaseversion.Service, kind, resourcePathValue string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		operation, err := service.Operation(
			request.Context(), kind, request.PathValue("projectID"), request.PathValue(resourcePathValue),
			request.PathValue("operationID"),
		)
		if writeDatabaseVersionError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicOperation(operation))
	}
}

func writeDatabaseVersionError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, databaseversion.ErrResourceBusy):
		writeAPIError(response, http.StatusConflict, "database_busy", "Managed database already has an active lifecycle operation")
	case errors.Is(err, databaseversion.ErrSameDigest):
		writeAPIError(response, http.StatusConflict, "database_image_already_active", "Selected image digest is already active")
	case errors.Is(err, state.ErrManagedRedisNotFound), errors.Is(err, state.ErrManagedPostgresNotFound):
		writeAPIError(response, http.StatusNotFound, "managed_database_not_found", "Managed database was not found")
	case errors.Is(err, state.ErrOperationNotFound):
		writeAPIError(response, http.StatusNotFound, "operation_not_found", "Version-change operation was not found")
	case errors.Is(err, databaseversion.ErrInvalidInput), errors.Is(err, databaseversion.ErrUnsupportedKind):
		writeAPIError(response, http.StatusBadRequest, "invalid_database_version_change", err.Error())
	default:
		writeAPIError(response, http.StatusUnprocessableEntity, "database_version_change_failed", err.Error())
	}
	return true
}
