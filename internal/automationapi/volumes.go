package automationapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

const maximumVolumeRequestBytes = 8 << 10

type volumeResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

func listVolumes(application *automation.VolumeApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		items, err := application.List(
			request.Context(), identity, request.PathValue("projectID"), request.PathValue("serviceID"),
		)
		if err != nil {
			writeVolumeError(response, err)
			return
		}
		result := make([]volumeResponse, 0, len(items))
		for _, item := range items {
			result = append(result, publicVolume(item))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func createVolume(application *automation.VolumeApplication) http.HandlerFunc {
	type requestBody struct {
		Name string `json:"name"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumVolumeRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid volume fields")
			return
		}
		result, err := application.Create(request.Context(), identity, automation.CreateVolumeInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Name: body.Name,
		})
		if err != nil {
			writeVolumeError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Volume.ProjectID+"/services/"+result.Volume.ServiceID+"/volumes/"+result.Volume.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicVolume(result.Volume))
	}
}

func deleteVolume(application *automation.VolumeApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		result, err := application.Delete(
			request.Context(), identity, request.PathValue("projectID"), request.PathValue("serviceID"), request.PathValue("volumeID"),
		)
		if err != nil {
			writeVolumeError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func publicVolume(item state.Volume) volumeResponse {
	return volumeResponse{
		ID: item.ID, ProjectID: item.ProjectID, ServiceID: item.ServiceID, Name: item.Name,
		CreatedAt: item.CreatedAtMillis,
	}
}

func writeVolumeError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, automation.ErrInvalidInput), errors.Is(err, volume.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_volume", err.Error())
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrServiceNotFound):
		writeError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrVolumeNotFound):
		writeError(response, http.StatusNotFound, "volume_not_found", "Volume not found")
	case errors.Is(err, state.ErrVolumeNameConflict):
		writeError(response, http.StatusConflict, "volume_name_conflict", "A volume with this name already exists for the service")
	case errors.Is(err, state.ErrVolumeInUse):
		writeError(response, http.StatusConflict, "volume_in_use", "Volume is referenced by desired or active service configuration")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to manage volume")
	}
}
