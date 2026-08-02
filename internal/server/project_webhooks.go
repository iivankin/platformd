package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/projectwebhook"
	"github.com/iivankin/platformd/internal/state"
)

const maximumProjectWebhookRequestBytes = 16 << 10

type projectWebhookResponse struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"projectId"`
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
}

type projectWebhookInput struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
}

func registerProjectWebhookRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/webhooks", listProjectWebhooks(config))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/webhooks", createProjectWebhook(config))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/webhooks/{webhookID}", updateProjectWebhook(config))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/webhooks/{webhookID}", deleteProjectWebhook(config))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/webhooks/test", testProjectWebhook(config))
}

func listProjectWebhooks(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		webhooks, err := config.projectWebhooks.List(request.Context(), request.PathValue("projectID"))
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load project webhooks")
			return
		}
		result := make([]projectWebhookResponse, 0, len(webhooks))
		for _, webhook := range webhooks {
			result = append(result, publicProjectWebhook(webhook))
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"eventTypes": projectwebhook.EventTypes,
			"webhooks":   result,
		})
	}
}

func createProjectWebhook(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body projectWebhookInput
		if !decodeProjectWebhookJSON(response, request, &body) {
			return
		}
		mutation, err := projectWebhookMutation(config, identity.Subject, identity.Email)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate project webhook identifiers")
			return
		}
		created, err := config.projectWebhooks.Create(request.Context(), request.PathValue("projectID"), body.URL, body.EventTypes, mutation)
		if writeProjectWebhookError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ProjectID+"/webhooks/"+created.ID)
		response.Header().Set("X-Request-ID", mutation.RequestCorrelationID)
		writeJSON(response, http.StatusCreated, publicProjectWebhook(created))
	}
}

func updateProjectWebhook(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body projectWebhookInput
		if !decodeProjectWebhookJSON(response, request, &body) {
			return
		}
		mutation, err := projectWebhookMutation(config, identity.Subject, identity.Email)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate project webhook identifiers")
			return
		}
		updated, err := config.projectWebhooks.Update(
			request.Context(), request.PathValue("projectID"), request.PathValue("webhookID"),
			body.URL, body.EventTypes, mutation,
		)
		if writeProjectWebhookError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", mutation.RequestCorrelationID)
		writeJSON(response, http.StatusOK, publicProjectWebhook(updated))
	}
}

func deleteProjectWebhook(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		mutation, err := projectWebhookMutation(config, identity.Subject, identity.Email)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate project webhook identifiers")
			return
		}
		err = config.projectWebhooks.Delete(request.Context(), request.PathValue("projectID"), request.PathValue("webhookID"), mutation)
		if writeProjectWebhookError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", mutation.RequestCorrelationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func testProjectWebhook(config handlerConfig) http.HandlerFunc {
	type testInput struct {
		URL string `json:"url"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		var body testInput
		if !decodeProjectWebhookJSON(response, request, &body) {
			return
		}
		err := config.projectWebhooks.Test(request.Context(), request.PathValue("projectID"), body.URL)
		switch {
		case errors.Is(err, projectwebhook.ErrInvalidURL):
			writeAPIError(response, http.StatusBadRequest, "invalid_webhook_url", "Webhook URL must be an HTTP or HTTPS URL without credentials or a fragment")
		case errors.Is(err, state.ErrProjectNotFound):
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
		case err != nil:
			writeAPIError(response, http.StatusBadGateway, "webhook_delivery_failed", err.Error())
		default:
			response.WriteHeader(http.StatusNoContent)
		}
	}
}

func decodeProjectWebhookJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumProjectWebhookRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid project webhook fields")
		return false
	}
	return true
}

func projectWebhookMutation(config handlerConfig, actorID, actorEmail string) (projectwebhook.Mutation, error) {
	_, auditID, correlationID, err := createRequestIDs()
	if err != nil {
		return projectwebhook.Mutation{}, err
	}
	return projectwebhook.Mutation{
		AuditEventID: auditID, ActorID: actorID, ActorEmail: actorEmail,
		RequestCorrelationID: correlationID, TimestampMillis: config.now().UnixMilli(),
	}, nil
}

func writeProjectWebhookError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, projectwebhook.ErrInvalidURL):
		writeAPIError(response, http.StatusBadRequest, "invalid_webhook_url", "Webhook URL must be an HTTP or HTTPS URL without credentials or a fragment")
	case errors.Is(err, projectwebhook.ErrInvalidEventType):
		writeAPIError(response, http.StatusBadRequest, "invalid_webhook_event", err.Error())
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrProjectWebhookNotFound):
		writeAPIError(response, http.StatusNotFound, "project_webhook_not_found", "Project webhook not found")
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to update project webhook", err)
	}
	return true
}

func publicProjectWebhook(webhook state.ProjectWebhook) projectWebhookResponse {
	return projectWebhookResponse{
		ID: webhook.ID, ProjectID: webhook.ProjectID, URL: webhook.URL,
		EventTypes: webhook.EventTypes, CreatedAt: webhook.CreatedAtMillis, UpdatedAt: webhook.UpdatedAtMillis,
	}
}
