package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/state"
)

type AuditRepository interface {
	AuditEvents(context.Context, state.AuditQuery) (state.AuditPage, error)
}

type auditEventResponse struct {
	ID                   string         `json:"id"`
	ActorKind            string         `json:"actorKind"`
	ActorID              string         `json:"actorId"`
	Action               string         `json:"action"`
	TargetKind           string         `json:"targetKind"`
	TargetID             string         `json:"targetId"`
	RequestCorrelationID string         `json:"requestCorrelationId,omitempty"`
	Result               string         `json:"result"`
	Metadata             map[string]any `json:"metadata"`
	CreatedAt            int64          `json:"createdAt"`
}

type auditPageResponse struct {
	Events     []auditEventResponse `json:"events"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

func registerAuditRoutes(mux *http.ServeMux, repository AuditRepository) {
	mux.HandleFunc("GET /api/v1/audit", listAuditEvents(repository))
}

func listAuditEvents(repository AuditRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		limit := 0
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_audit_page", "limit must be an integer")
				return
			}
			limit = parsed
		}
		page, err := repository.AuditEvents(request.Context(), state.AuditQuery{
			ActorKind: request.URL.Query().Get("actorKind"), Action: request.URL.Query().Get("action"),
			Result: request.URL.Query().Get("result"), Cursor: request.URL.Query().Get("cursor"), Limit: limit,
		})
		if errors.Is(err, state.ErrAuditPageInvalid) || errors.Is(err, state.ErrAuditCursorInvalid) {
			writeAPIError(response, http.StatusBadRequest, "invalid_audit_page", err.Error())
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "audit_read_failed", "Unable to read audit history")
			return
		}
		events := make([]auditEventResponse, 0, len(page.Events))
		for _, event := range page.Events {
			events = append(events, auditEventResponse{
				ID: event.ID, ActorKind: event.ActorKind, ActorID: event.ActorID,
				Action: event.Action, TargetKind: event.TargetKind, TargetID: event.TargetID,
				RequestCorrelationID: event.RequestCorrelationID, Result: event.Result,
				Metadata: event.Metadata, CreatedAt: event.CreatedAtMillis,
			})
		}
		writeJSON(response, http.StatusOK, auditPageResponse{Events: events, NextCursor: page.NextCursor})
	}
}
