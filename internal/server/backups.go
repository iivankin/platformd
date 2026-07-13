package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/state"
)

const maximumBackupTargetRequestBytes = 32 << 10

type backupTargetResponse struct {
	Configured  bool   `json:"configured"`
	Endpoint    string `json:"endpoint,omitempty"`
	Region      string `json:"region,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	AccessKeyID string `json:"accessKeyId,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

func registerBackupTargetRoutes(mux *http.ServeMux, application *backup.TargetApplication) {
	mux.HandleFunc("GET /api/v1/backups/target", getBackupTarget(application))
	mux.HandleFunc("PUT /api/v1/backups/target", setBackupTarget(application))
	mux.HandleFunc("DELETE /api/v1/backups/target", deleteBackupTarget(application))
}

func getBackupTarget(application *backup.TargetApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		target, configured, err := application.Target(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load backup target")
			return
		}
		writeJSON(response, http.StatusOK, publicBackupTarget(target, configured))
	}
}

func setBackupTarget(application *backup.TargetApplication) http.HandlerFunc {
	type requestBody struct {
		Endpoint        string `json:"endpoint"`
		Region          string `json:"region"`
		Bucket          string `json:"bucket"`
		Prefix          string `json:"prefix"`
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeBackupJSON(response, request, &body) {
			return
		}
		result, err := application.SetTarget(request.Context(), backup.TargetInput{
			Endpoint: body.Endpoint, Region: body.Region, Bucket: body.Bucket,
			Prefix: body.Prefix, AccessKeyID: body.AccessKeyID, SecretAccessKey: body.SecretAccessKey,
			Actor: backup.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeBackupTargetError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, publicBackupTarget(result.Target, true))
	}
}

func deleteBackupTarget(application *backup.TargetApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		requestID, err := application.DeleteTarget(request.Context(), backup.Actor{
			Kind: "access", ID: identity.Subject, Email: identity.Email,
		})
		if writeBackupTargetError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeBackupJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumBackupTargetRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid backup target fields")
		return false
	}
	return true
}

func writeBackupTargetError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, backup.ErrTargetBusy):
		writeAPIError(response, http.StatusConflict, "backup_target_busy", "A backup target or backup action is already running")
	case errors.Is(err, backup.ErrEmbeddedTarget):
		writeAPIError(response, http.StatusUnprocessableEntity, "embedded_backup_target", err.Error())
	case errors.Is(err, backup.ErrInvalidInput):
		writeAPIError(response, http.StatusBadRequest, "invalid_backup_target", err.Error())
	case errors.Is(err, state.ErrBackupTargetNotFound):
		writeAPIError(response, http.StatusNotFound, "backup_target_not_found", "Backup target is not configured")
	default:
		writeAPIError(response, http.StatusUnprocessableEntity, "backup_target_probe_failed", err.Error())
	}
	return true
}

func publicBackupTarget(target backup.Target, configured bool) backupTargetResponse {
	return backupTargetResponse{
		Configured: configured, Endpoint: target.Endpoint, Region: target.Region,
		Bucket: target.Bucket, Prefix: target.Prefix, AccessKeyID: target.AccessKeyID,
		CreatedAt: target.CreatedAtMillis, UpdatedAt: target.UpdatedAtMillis,
	}
}
