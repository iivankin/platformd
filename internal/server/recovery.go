package server

import (
	"context"
	"net/http"
)

type RecoveryResource struct {
	ResourceKind      string `json:"resourceKind"`
	ResourceID        string `json:"resourceId"`
	Status            string `json:"status"`
	GenerationID      string `json:"generationId,omitempty"`
	SourceCompletedAt int64  `json:"sourceCompletedAt,omitempty"`
}

type RecoveryStatus struct {
	Resources []RecoveryResource `json:"resources"`
	LastError string             `json:"lastError,omitempty"`
}

type RecoveryRepository interface {
	RecoveryStatus(context.Context) (RecoveryStatus, error)
	RetryRecovery()
}

func registerRecoveryRoutes(mux *http.ServeMux, repository RecoveryRepository) {
	mux.HandleFunc("GET /api/v1/recovery", func(response http.ResponseWriter, request *http.Request) {
		status, err := repository.RecoveryStatus(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load recovery status")
			return
		}
		writeJSON(response, http.StatusOK, status)
	})
	mux.HandleFunc("POST /api/v1/recovery/retry", func(response http.ResponseWriter, _ *http.Request) {
		repository.RetryRecovery()
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusAccepted)
	})
}
