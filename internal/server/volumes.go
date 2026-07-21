package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

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

func registerVolumeRoutes(mux *http.ServeMux, application *volume.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/volumes", listVolumes(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/volumes", createVolume(application))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/services/{serviceID}/volumes/{volumeID}", deleteVolume(application))
}

func listVolumes(application *volume.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		volumes, err := application.List(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeVolumeError(response, err)
			return
		}
		result := make([]volumeResponse, 0, len(volumes))
		for _, item := range volumes {
			result = append(result, publicVolume(item))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func createVolume(application *volume.Application) http.HandlerFunc {
	type requestBody struct {
		Name string `json:"name"`
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
		request.Body = http.MaxBytesReader(response, request.Body, maximumVolumeRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid volume fields")
			return
		}
		result, err := application.Create(request.Context(), volume.CreateInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			Name:  body.Name,
			Actor: volume.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
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

func deleteVolume(application *volume.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		result, err := application.Delete(request.Context(), volume.DeleteInput{
			ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
			VolumeID: request.PathValue("volumeID"),
			Actor:    volume.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
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
	case errors.Is(err, volume.ErrInvalidInput):
		writeAPIError(response, http.StatusBadRequest, "invalid_volume", err.Error())
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrVolumeNotFound):
		writeAPIError(response, http.StatusNotFound, "volume_not_found", "Volume not found")
	case errors.Is(err, state.ErrVolumeNameConflict):
		writeAPIError(response, http.StatusConflict, "volume_name_conflict", "A volume with this name already exists for the service")
	case errors.Is(err, state.ErrVolumeInUse):
		writeAPIError(response, http.StatusConflict, "volume_in_use", "Volume is referenced by desired or active service configuration")
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage volume")
	}
}
