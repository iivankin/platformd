package automationapi

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/rootexec"
)

type serverExecRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type serverExecResponse struct {
	rootexec.Result
	RequestID string `json:"requestId"`
}

func executeServerCommand(application *automation.ServerExecApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		var body serverExecRequest
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Execute(request.Context(), identity, automation.ServerExecInput{
			Command: body.Command, TimeoutSeconds: body.TimeoutSeconds,
		})
		if writeServerExecError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusOK, serverExecResponse{
			Result: result.Execution, RequestID: result.RequestID,
		})
	}
}

func writeServerExecError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, automation.ErrUnboundAdminRequired):
		writeError(response, http.StatusForbidden, "unbound_admin_required", "Server exec requires an unbound admin token")
	case errors.Is(err, rootexec.ErrInvalidRequest):
		writeError(response, http.StatusBadRequest, "invalid_server_exec", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "server_exec_failed", "Server command execution failed")
	}
	return true
}
