package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/selfupdate"
)

type SelfUpdater interface {
	Check(context.Context) (selfupdate.Status, error)
	Apply(context.Context) (selfupdate.Result, error)
}

func registerSelfUpdateRoute(mux *http.ServeMux, updater SelfUpdater, afterCommit func()) {
	mux.HandleFunc("GET /api/v1/infrastructure/update", func(response http.ResponseWriter, request *http.Request) {
		status, err := updater.Check(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusBadGateway, "update_check_failed", "Unable to check for platform updates")
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, status)
	})
	mux.HandleFunc("POST /api/v1/infrastructure/update", func(response http.ResponseWriter, request *http.Request) {
		result, err := updater.Apply(request.Context())
		if err != nil {
			var busy selfupdate.BusyError
			switch {
			case errors.Is(err, selfupdate.ErrUpToDate):
				writeAPIError(response, http.StatusConflict, "already_up_to_date", "Platform is already up to date")
			case errors.As(err, &busy):
				writeJSON(response, http.StatusConflict, map[string]any{
					"error": map[string]string{
						"code": "platform_busy", "message": "Platform has active operations",
					},
					"blockers": busy.Snapshot.Blockers, "total": busy.Snapshot.Total,
					"truncated": busy.Snapshot.Truncated,
				})
			case errors.Is(err, admission.ErrUpdating):
				writeAPIError(response, http.StatusConflict, "platform_updating", "Platform update is already in progress")
			default:
				writeAPIError(response, http.StatusBadGateway, "update_failed", "Unable to verify and install the platform update")
			}
			return
		}
		writeJSON(response, http.StatusAccepted, result)
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		afterCommit()
	})
}
