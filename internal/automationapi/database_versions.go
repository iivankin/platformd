package automationapi

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/state"
)

type databaseVersionRequest struct {
	ImageTag string `json:"imageTag"`
}

type databaseVersionOperation struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	TargetID     string `json:"targetId"`
	Status       string `json:"status"`
	Progress     string `json:"progress,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	StartedAt    int64  `json:"startedAt"`
	FinishedAt   *int64 `json:"finishedAt,omitempty"`
}

func startDatabaseVersionChange(service *databaseversion.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body databaseVersionRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := service.Start(
			request.Context(), request.PathValue("kind"), request.PathValue("projectID"),
			request.PathValue("resourceID"), body.ImageTag,
			databaseversion.Actor{Kind: "token", ID: identity.TokenID},
		)
		if writeAutomationDatabaseVersionError(response, err) {
			return
		}
		response.Header().Set("Location", request.URL.Path+"/"+result.Operation.ID)
		writeJSON(response, http.StatusAccepted, map[string]any{
			"operation": publicDatabaseVersionOperation(result.Operation),
			"sourceTag": result.SourceTag, "sourceDigest": result.SourceDigest,
			"targetTag": result.TargetTag, "targetDigest": result.TargetDigest,
		})
	}
}

func readDatabaseVersionChange(service *databaseversion.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !requireProject(response, request, request.PathValue("projectID")) {
			return
		}
		operation, err := service.Operation(
			request.Context(), request.PathValue("kind"), request.PathValue("projectID"),
			request.PathValue("resourceID"), request.PathValue("operationID"),
		)
		if writeAutomationDatabaseVersionError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicDatabaseVersionOperation(operation))
	}
}

func publicDatabaseVersionOperation(operation state.Operation) databaseVersionOperation {
	result := databaseVersionOperation{
		ID: operation.ID, Kind: operation.Kind, TargetID: operation.TargetID,
		Status: operation.Status, Progress: operation.Progress, ErrorCode: operation.ErrorCode,
		ErrorMessage: operation.ErrorMessage, StartedAt: operation.StartedAtMillis,
	}
	if operation.FinishedAtMillis > 0 {
		result.FinishedAt = &operation.FinishedAtMillis
	}
	return result
}

func writeAutomationDatabaseVersionError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, databaseversion.ErrResourceBusy):
		writeError(response, http.StatusConflict, "database_busy", "Managed database already has an active lifecycle operation")
	case errors.Is(err, databaseversion.ErrSameDigest):
		writeError(response, http.StatusConflict, "database_image_already_active", "Selected image digest is already active")
	case errors.Is(err, databaseversion.ErrInvalidInput), errors.Is(err, databaseversion.ErrUnsupportedKind):
		writeError(response, http.StatusBadRequest, "invalid_database_version_change", err.Error())
	case errors.Is(err, state.ErrManagedPostgresNotFound), errors.Is(err, state.ErrManagedRedisNotFound):
		writeError(response, http.StatusNotFound, "managed_database_not_found", "Managed database not found")
	case errors.Is(err, state.ErrOperationNotFound):
		writeError(response, http.StatusNotFound, "operation_not_found", "Version-change operation not found")
	default:
		writeError(response, http.StatusUnprocessableEntity, "database_version_change_failed", "Unable to start or read the managed database version change")
	}
	return true
}
