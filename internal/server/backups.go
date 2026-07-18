package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/state"
)

const maximumBackupTargetRequestBytes = 32 << 10

type backupTargetResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"accessKeyId"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type backupPolicyResponse struct {
	ResourceKind   string `json:"resourceKind"`
	ResourceID     string `json:"resourceId"`
	TargetID       string `json:"targetId,omitempty"`
	Enabled        bool   `json:"enabled"`
	Cron           string `json:"cron,omitempty"`
	RetentionCount int    `json:"retentionCount"`
	NextRunAt      int64  `json:"nextRunAt,omitempty"`
}

type backupRecordResponse struct {
	ID                  string `json:"id"`
	TargetID            string `json:"targetId"`
	ResourceKind        string `json:"resourceKind"`
	ResourceID          string `json:"resourceId"`
	ScheduledOccurrence *int64 `json:"scheduledOccurrence,omitempty"`
	GenerationID        string `json:"generationId,omitempty"`
	Status              string `json:"status"`
	SizeBytes           *int64 `json:"sizeBytes,omitempty"`
	ErrorCode           string `json:"errorCode,omitempty"`
	ErrorMessage        string `json:"errorMessage,omitempty"`
	StartedAt           int64  `json:"startedAt"`
	FinishedAt          *int64 `json:"finishedAt,omitempty"`
}

type backupGenerationResponse struct {
	GenerationID  string `json:"generationId"`
	PlaintextSize int64  `json:"plaintextSize"`
	RemoteSize    int64  `json:"remoteSize"`
	CompletedAt   int64  `json:"completedAt"`
}

type operationResponse struct {
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

func registerBackupTargetRoutes(mux *http.ServeMux, application *backup.TargetApplication) {
	mux.HandleFunc("GET /api/v1/backups/targets", getBackupTargets(application))
	mux.HandleFunc("POST /api/v1/backups/targets", setBackupTarget(application, false))
	mux.HandleFunc("PUT /api/v1/backups/targets/{targetID}", setBackupTarget(application, true))
	mux.HandleFunc("DELETE /api/v1/backups/targets/{targetID}", deleteBackupTarget(application))
	mux.HandleFunc("PUT /api/v1/backups/control-target", setControlBackupTarget(application))
}

func registerBackupResourceRoutes(mux *http.ServeMux, application *backup.ResourceApplication) {
	mux.HandleFunc("GET /api/v1/backups/resources", listBackupPolicies(application))
	mux.HandleFunc("GET /api/v1/backups/resources/{kind}/{resourceID}/policy", getBackupPolicy(application))
	mux.HandleFunc("PUT /api/v1/backups/resources/{kind}/{resourceID}/policy", setBackupPolicy(application))
	mux.HandleFunc("POST /api/v1/backups/resources/{kind}/{resourceID}/run", runBackupNow(application))
	mux.HandleFunc("GET /api/v1/backups/resources/{kind}/{resourceID}/history", getBackupHistory(application))
	mux.HandleFunc("GET /api/v1/backups/resources/{kind}/{resourceID}/generations", getBackupGenerations(application))
	mux.HandleFunc("POST /api/v1/backups/resources/{kind}/{resourceID}/restore", restoreBackupGeneration(application))
	mux.HandleFunc("GET /api/v1/operations/{operationID}", getOperation(application))
}

func restoreBackupGeneration(application *backup.ResourceApplication) http.HandlerFunc {
	type requestBody struct {
		TargetID             string `json:"targetId"`
		GenerationID         string `json:"generationId"`
		Mode                 string `json:"mode"`
		NewResourceName      string `json:"newResourceName"`
		DestructiveConfirmed bool   `json:"destructiveConfirmed"`
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
		operation, err := application.Restore(
			request.Context(), request.PathValue("kind"), request.PathValue("resourceID"), body.TargetID, body.GenerationID,
			backup.ResourceRestoreOptions{
				Mode: body.Mode, NewResourceName: body.NewResourceName,
				DestructiveConfirmed: body.DestructiveConfirmed,
			},
			backup.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		)
		if writeBackupResourceError(response, err) {
			return
		}
		writeJSON(response, http.StatusAccepted, publicOperation(operation))
	}
}

func getOperation(application *backup.ResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		operation, err := application.Operation(request.Context(), request.PathValue("operationID"))
		if errors.Is(err, state.ErrOperationNotFound) {
			writeAPIError(response, http.StatusNotFound, "operation_not_found", "Operation was not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load operation")
			return
		}
		writeJSON(response, http.StatusOK, publicOperation(operation))
	}
}

func publicOperation(operation state.Operation) operationResponse {
	result := operationResponse{
		ID: operation.ID, Kind: operation.Kind, TargetID: operation.TargetID,
		Status: operation.Status, Progress: operation.Progress, ErrorCode: operation.ErrorCode,
		ErrorMessage: operation.ErrorMessage, StartedAt: operation.StartedAtMillis,
	}
	if operation.FinishedAtMillis > 0 {
		result.FinishedAt = &operation.FinishedAtMillis
	}
	return result
}

func getBackupGenerations(application *backup.ResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		generations, err := application.Generations(
			request.Context(), request.PathValue("kind"), request.PathValue("resourceID"), request.URL.Query().Get("targetId"),
		)
		if writeBackupResourceError(response, err) {
			return
		}
		result := make([]backupGenerationResponse, len(generations))
		for index, generation := range generations {
			result[index] = backupGenerationResponse{
				GenerationID: generation.GenerationID, PlaintextSize: generation.PlaintextSize,
				RemoteSize: generation.RemoteSize, CompletedAt: generation.CompletedAtMillis,
			}
		}
		writeJSON(response, http.StatusOK, map[string]any{"generations": result})
	}
}

func listBackupPolicies(application *backup.ResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		policies, err := application.Policies(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load backup policies")
			return
		}
		result := make([]backupPolicyResponse, len(policies))
		for index, policy := range policies {
			result[index] = publicBackupPolicy(policy)
		}
		writeJSON(response, http.StatusOK, map[string]any{"policies": result})
	}
}

func getBackupPolicy(application *backup.ResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		policy, err := application.Policy(request.Context(), request.PathValue("kind"), request.PathValue("resourceID"))
		if writeBackupResourceError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicBackupPolicy(policy))
	}
}

func setBackupPolicy(application *backup.ResourceApplication) http.HandlerFunc {
	type requestBody struct {
		TargetID       string `json:"targetId"`
		Enabled        bool   `json:"enabled"`
		Cron           string `json:"cron"`
		RetentionCount int    `json:"retentionCount"`
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
		result, err := application.SetPolicy(request.Context(), backup.PolicyInput{
			ResourceKind: request.PathValue("kind"), ResourceID: request.PathValue("resourceID"),
			TargetID: body.TargetID, Enabled: body.Enabled, Cron: body.Cron, RetentionCount: body.RetentionCount,
			Actor: backup.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeBackupResourceError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, publicBackupPolicy(backup.PolicyStatus{
			Policy: result.Policy, NextRunAtMillis: result.NextRunAtMillis,
		}))
	}
}

func runBackupNow(application *backup.ResourceApplication) http.HandlerFunc {
	type requestBody struct {
		TargetID string `json:"targetId"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeBackupJSON(response, request, &body) {
			return
		}
		record, err := application.RunNow(request.Context(), request.PathValue("kind"), request.PathValue("resourceID"), body.TargetID)
		if writeBackupResourceError(response, err) {
			return
		}
		writeJSON(response, http.StatusAccepted, publicBackupRecord(record))
	}
}

func getBackupHistory(application *backup.ResourceApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		limit := 50
		before := int64(0)
		var err error
		if value := request.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
		}
		if err == nil {
			if value := request.URL.Query().Get("before"); value != "" {
				before, err = strconv.ParseInt(value, 10, 64)
			}
		}
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_pagination", "Backup history pagination is invalid")
			return
		}
		records, err := application.History(
			request.Context(), request.PathValue("kind"), request.PathValue("resourceID"),
			request.URL.Query().Get("targetId"), before, limit,
		)
		if writeBackupResourceError(response, err) {
			return
		}
		result := make([]backupRecordResponse, len(records))
		for index, record := range records {
			result[index] = publicBackupRecord(record)
		}
		writeJSON(response, http.StatusOK, map[string]any{"backups": result})
	}
}

func publicBackupPolicy(status backup.PolicyStatus) backupPolicyResponse {
	policy := status.Policy
	return backupPolicyResponse{
		ResourceKind: policy.ResourceKind, ResourceID: policy.ResourceID, Enabled: policy.Enabled,
		TargetID: policy.TargetID, Cron: policy.Cron, RetentionCount: policy.RetentionCount, NextRunAt: status.NextRunAtMillis,
	}
}

func publicBackupRecord(record state.BackupRecord) backupRecordResponse {
	return backupRecordResponse{
		ID: record.ID, TargetID: record.TargetID, ResourceKind: record.ResourceKind, ResourceID: record.ResourceID,
		ScheduledOccurrence: record.ScheduledOccurrenceMillis, GenerationID: record.GenerationID,
		Status: record.Status, SizeBytes: record.SizeBytes, ErrorCode: record.ErrorCode,
		ErrorMessage: record.ErrorMessage, StartedAt: record.StartedAtMillis, FinishedAt: record.FinishedAtMillis,
	}
}

func writeBackupResourceError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrBackupResourceNotFound):
		writeAPIError(response, http.StatusNotFound, "backup_resource_not_found", "Backup resource was not found")
	case errors.Is(err, backup.ErrWorkerBusy):
		writeAPIError(response, http.StatusConflict, "backup_worker_busy", "Another backup is already running")
	case errors.Is(err, backup.ErrResourceTargetMissing):
		writeAPIError(response, http.StatusUnprocessableEntity, "backup_target_not_found", "Backup target is not configured")
	case errors.Is(err, backup.ErrTargetBusy):
		writeAPIError(response, http.StatusConflict, "backup_target_busy", "Backup target is busy")
	case errors.Is(err, backup.ErrResourceGenerationNotFound):
		writeAPIError(response, http.StatusNotFound, "backup_generation_not_found", "Backup generation was not found")
	case errors.Is(err, backup.ErrResourceRestorer):
		writeAPIError(response, http.StatusUnprocessableEntity, "restore_not_available", "Restore is not available for this resource")
	default:
		writeAPIError(response, http.StatusBadRequest, "invalid_backup_resource", err.Error())
	}
	return true
}

func getBackupTargets(application *backup.TargetApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		targets, err := application.Targets(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load backup target")
			return
		}
		controlTargetID, err := application.ControlTargetID(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load disaster recovery storage")
			return
		}
		result := make([]backupTargetResponse, len(targets))
		for index := range targets {
			result[index] = publicBackupTarget(targets[index])
		}
		writeJSON(response, http.StatusOK, map[string]any{"targets": result, "controlTargetId": controlTargetID})
	}
}

func setBackupTarget(application *backup.TargetApplication, update bool) http.HandlerFunc {
	type requestBody struct {
		Name            string `json:"name"`
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
			ID: func() string {
				if update {
					return request.PathValue("targetID")
				}
				return ""
			}(), Name: body.Name,
			Endpoint: body.Endpoint, Region: body.Region, Bucket: body.Bucket,
			Prefix: body.Prefix, AccessKeyID: body.AccessKeyID, SecretAccessKey: body.SecretAccessKey,
			Actor: backup.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeBackupTargetError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, publicBackupTarget(result.Target))
	}
}

func deleteBackupTarget(application *backup.TargetApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		requestID, err := application.DeleteTarget(request.Context(), request.PathValue("targetID"), backup.Actor{
			Kind: "access", ID: identity.Subject, Email: identity.Email,
		})
		if writeBackupTargetError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func setControlBackupTarget(application *backup.TargetApplication) http.HandlerFunc {
	type requestBody struct {
		TargetID string `json:"targetId"`
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
		result, err := application.SetControlTarget(request.Context(), body.TargetID, backup.Actor{
			Kind: "access", ID: identity.Subject, Email: identity.Email,
		})
		if writeBackupTargetError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, map[string]string{"targetId": result.TargetID})
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
	case errors.Is(err, state.ErrBackupTargetInUse):
		writeAPIError(response, http.StatusConflict, "backup_target_in_use", "Backup storage is selected by a policy or disaster recovery")
	default:
		writeAPIError(response, http.StatusUnprocessableEntity, "backup_target_probe_failed", err.Error())
	}
	return true
}

func publicBackupTarget(target backup.Target) backupTargetResponse {
	return backupTargetResponse{
		ID: target.ID, Name: target.Name, Endpoint: target.Endpoint, Region: target.Region,
		Bucket: target.Bucket, Prefix: target.Prefix, AccessKeyID: target.AccessKeyID,
		CreatedAt: target.CreatedAtMillis, UpdatedAt: target.UpdatedAtMillis,
	}
}
