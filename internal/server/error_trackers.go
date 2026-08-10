package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/errortracker"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
)

const maximumErrorTrackerRequestBytes = 32 << 10

type errorTrackerResponse struct {
	ID                   string `json:"id"`
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	VolumeID             string `json:"volumeId"`
	InternalHostname     string `json:"internalHostname"`
	InternalURL          string `json:"internalUrl"`
	PublicHostname       string `json:"publicHostname,omitempty"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	Status               string `json:"status"`
	StatusMessage        string `json:"statusMessage,omitempty"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}

func registerErrorTrackerRoutes(mux *http.ServeMux, application *errortracker.Application, proxy *errortracker.Proxy) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/error-trackers", listErrorTrackers(application))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/error-trackers", createErrorTracker(application))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/error-trackers/{trackerID}", getErrorTracker(application))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/error-trackers/{trackerID}/public-access", updateErrorTrackerPublicAccess(application))
	mux.Handle("/api/v1/projects/{projectID}/error-trackers/{trackerID}/console/{path...}", errorTrackerConsole(application, proxy))
}

func listErrorTrackers(application *errortracker.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		trackers, err := application.Trackers(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeErrorTrackerError(response, err)
			return
		}
		result := make([]errorTrackerResponse, len(trackers))
		for index, tracker := range trackers {
			result[index] = publicErrorTracker(application, tracker)
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getErrorTracker(application *errortracker.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		tracker, err := application.Tracker(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeErrorTrackerError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicErrorTracker(application, tracker))
	}
}

func createErrorTracker(application *errortracker.Application) http.HandlerFunc {
	type requestBody struct {
		Name           string                     `json:"name"`
		PublicHostname string                     `json:"publicHostname"`
		BackupPolicy   initialBackupPolicyRequest `json:"backupPolicy"`
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
		request.Body = http.MaxBytesReader(response, request.Body, maximumErrorTrackerRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid error tracker fields")
			return
		}
		created, requestID, err := application.Create(request.Context(), errortracker.CreateInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, PublicHostname: body.PublicHostname,
			BackupPolicy: body.BackupPolicy.statePolicy(),
			Actor:        errortracker.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeErrorTrackerError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ProjectID+"/error-trackers/"+created.ID)
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusCreated, publicErrorTracker(application, created))
	}
}

func updateErrorTrackerPublicAccess(application *errortracker.Application) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
		PublicHostname    string `json:"publicHostname"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumErrorTrackerRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_error_tracker", "Error tracker public access fields are invalid")
			return
		}
		updated, err := application.UpdatePublicAccess(
			request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"),
			body.PublicHostname, body.ExpectedUpdatedAt,
		)
		if err != nil {
			writeErrorTrackerError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicErrorTracker(application, updated))
	}
}

func errorTrackerConsole(application *errortracker.Application, proxy *errortracker.Proxy) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if proxy == nil {
			http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		tracker, err := application.Tracker(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeErrorTrackerError(response, err)
			return
		}
		forwarded := request.Clone(request.Context())
		forwarded.URL.Path = "/" + request.PathValue("path")
		forwarded.URL.RawPath = ""
		forwarded.Header = request.Header.Clone()
		forwarded.Header.Del("Authorization")
		forwarded.Header.Del("Cookie")
		forwarded.Header.Del("Cf-Access-Jwt-Assertion")
		proxy.Serve(response, forwarded, tracker.ID)
	})
}

func publicErrorTracker(application *errortracker.Application, tracker state.ErrorTracker) errorTrackerResponse {
	hostname := tracker.Name + "." + tracker.ProjectName + ".internal"
	status, statusMessage := application.Status(tracker.ID)
	return errorTrackerResponse{
		ID: tracker.ID, ProjectID: tracker.ProjectID, Name: tracker.Name, VolumeID: tracker.VolumeID,
		InternalHostname: hostname,
		InternalURL:      "http://" + hostname + ":" + strconv.Itoa(firewall.ErrorTrackerPort),
		PublicHostname:   tracker.PublicHostname, BackupEnabled: tracker.BackupEnabled,
		BackupCron: tracker.BackupCron, BackupRetentionCount: tracker.BackupRetentionCount,
		Status: status, StatusMessage: statusMessage,
		CreatedAt: tracker.CreatedAtMillis, UpdatedAt: tracker.UpdatedAtMillis,
	}
}

func writeErrorTrackerError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project was not found")
	case errors.Is(err, state.ErrErrorTrackerNotFound):
		writeAPIError(response, http.StatusNotFound, "error_tracker_not_found", "Error tracker was not found")
	case errors.Is(err, state.ErrResourceNameConflict), errors.Is(err, state.ErrHostnameInUse), errors.Is(err, state.ErrErrorTrackerChanged):
		writeAPIError(response, http.StatusConflict, "error_tracker_conflict", err.Error())
	case errors.Is(err, state.ErrCertificateCoverage):
		writeAPIError(response, http.StatusUnprocessableEntity, "certificate_coverage", err.Error())
	case errors.Is(err, errortracker.ErrInvalidInput), errors.Is(err, state.ErrInvalidBackupPolicy):
		writeAPIError(response, http.StatusBadRequest, "invalid_error_tracker", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage error tracker")
	}
}
