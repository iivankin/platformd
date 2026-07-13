package automationapi

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

func createProject(application *automation.ProjectApplication) http.HandlerFunc {
	type requestBody struct {
		Name string `json:"name"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Create(request.Context(), identity, body.Name)
		switch {
		case errors.Is(err, automation.ErrAdminRequired):
			writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
		case errors.Is(err, automation.ErrProjectBoundary):
			writeError(response, http.StatusForbidden, "unbound_admin_required", "Project creation requires an unbound admin token")
		case errors.Is(err, automation.ErrInvalidInput):
			writeError(response, http.StatusBadRequest, "invalid_project", err.Error())
		case errors.Is(err, state.ErrProjectNameConflict):
			writeError(response, http.StatusConflict, "project_name_conflict", "A project with this name already exists")
		case err != nil:
			writeError(response, http.StatusInternalServerError, "internal_error", "Unable to create project")
		default:
			response.Header().Set("Location", "/api/v1/projects/"+result.Project.ID)
			response.Header().Set("X-Request-ID", result.RequestID)
			writeJSON(response, http.StatusCreated, publicProject(result.Project))
		}
	}
}

func requireUnboundAdmin(response http.ResponseWriter, request *http.Request) (automation.Identity, bool) {
	identity, ok := requireIdentity(response, request)
	if !ok {
		return automation.Identity{}, false
	}
	if !identity.IsAdmin() {
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
		return automation.Identity{}, false
	}
	if identity.ProjectID != nil {
		writeError(response, http.StatusForbidden, "unbound_admin_required", "Project creation requires an unbound admin token")
		return automation.Identity{}, false
	}
	return identity, true
}
