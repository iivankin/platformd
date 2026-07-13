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
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis/{redisID}/version-change/preview", previewDatabaseVersionChange(service, databaseversion.Redis, "redisID"))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis/{redisID}/version-change", startDatabaseVersionChange(service, databaseversion.Redis, "redisID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/version-change/{operationID}", readDatabaseVersionChange(service, databaseversion.Redis, "redisID"))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres/{postgresID}/version-change/preview", previewDatabaseVersionChange(service, databaseversion.Postgres, "postgresID"))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres/{postgresID}/version-change", startDatabaseVersionChange(service, databaseversion.Postgres, "postgresID"))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}/version-change/{operationID}", readDatabaseVersionChange(service, databaseversion.Postgres, "postgresID"))
}

func previewDatabaseVersionChange(service *databaseversion.Service, kind, resourcePathValue string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		body, ok := decodeDatabaseVersionPreviewRequest(response, request)
		if !ok {
			return
		}
		preview, err := service.Preview(
			request.Context(), kind, request.PathValue("projectID"),
			request.PathValue(resourcePathValue), body.ImageTag,
		)
		if writeDatabaseVersionError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, preview)
	}
}

func startDatabaseVersionChange(service *databaseversion.Service, kind, resourcePathValue string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		body, ok := decodeDatabaseVersionStartRequest(response, request)
		if !ok {
			return
		}
		result, err := service.Start(
			request.Context(), kind, request.PathValue("projectID"), request.PathValue(resourcePathValue),
			body.ImageTag, body.ExpectedTargetDigest,
			databaseversion.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
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

type databaseVersionPreviewRequest struct {
	ImageTag string `json:"imageTag"`
}

type databaseVersionStartRequest struct {
	ImageTag             string `json:"imageTag"`
	ExpectedTargetDigest string `json:"expectedTargetDigest"`
}

func decodeDatabaseVersionPreviewRequest(response http.ResponseWriter, request *http.Request) (databaseVersionPreviewRequest, bool) {
	var body databaseVersionPreviewRequest
	ok := decodeDatabaseVersionRequest(response, request, &body, "imageTag")
	return body, ok
}

func decodeDatabaseVersionStartRequest(response http.ResponseWriter, request *http.Request) (databaseVersionStartRequest, bool) {
	var body databaseVersionStartRequest
	ok := decodeDatabaseVersionRequest(response, request, &body, "imageTag and expectedTargetDigest")
	return body, ok
}

func decodeDatabaseVersionRequest(response http.ResponseWriter, request *http.Request, body any, fields string) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumDatabaseVersionRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_database_version_change", "Request body must contain only "+fields)
		return false
	}
	return true
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
	case errors.Is(err, databaseversion.ErrTargetDigestMoved):
		writeAPIError(response, http.StatusConflict, "database_target_digest_changed", "Selected tag moved after preview; preview it again before starting")
	case errors.Is(err, databaseversion.ErrInsufficientSpace):
		writeAPIError(response, http.StatusInsufficientStorage, "database_version_space_insufficient", "Managed database version change needs more free disk space")
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
