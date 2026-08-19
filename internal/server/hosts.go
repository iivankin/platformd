package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/iivankin/platformd/internal/state"
)

type HostRepository interface {
	Hosts(context.Context) ([]state.Host, error)
	ActiveHostJoinTokens(context.Context) ([]state.HostJoinToken, error)
	CreateJoinToken(context.Context, string, string, string, string) (string, state.HostJoinToken, error)
	JoinCommand(string) string
	DeleteHost(context.Context, state.DeleteHostInput) error
	DeleteJoinToken(context.Context, state.DeleteHostJoinTokenInput) error
}

type hostResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicIPv4 string `json:"publicIpv4,omitempty"`
	LastSeenAt *int64 `json:"lastSeenAt,omitempty"`
	JoinedAt   int64  `json:"joinedAt"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	Connected  bool   `json:"connected"`
}

type hostJoinTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`
	Command   string `json:"command,omitempty"`
	ExpiresAt int64  `json:"expiresAt"`
	CreatedAt int64  `json:"createdAt"`
}

type hostConnected interface {
	Connected(string) bool
}

func registerHostRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/hosts", listHosts(config))
	mux.HandleFunc("DELETE /api/v1/hosts/{hostID}", deleteHost(config))
	mux.HandleFunc("GET /api/v1/hosts/join-tokens", listHostJoinTokens(config))
	mux.HandleFunc("POST /api/v1/hosts/join-tokens", createHostJoinToken(config))
	mux.HandleFunc("DELETE /api/v1/hosts/join-tokens/{tokenID}", deleteHostJoinToken(config))
}

func listHosts(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		hosts, err := config.hosts.Hosts(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list child servers")
			return
		}
		connected, _ := config.hosts.(hostConnected)
		result := make([]hostResponse, 0, len(hosts))
		for _, host := range hosts {
			item := publicHost(host)
			if connected != nil {
				item.Connected = connected.Connected(host.ID)
			}
			result = append(result, item)
		}
		writeJSON(response, http.StatusOK, map[string]any{"hosts": result})
	}
}

func listHostJoinTokens(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		tokens, err := config.hosts.ActiveHostJoinTokens(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list join tokens")
			return
		}
		result := make([]hostJoinTokenResponse, 0, len(tokens))
		for _, token := range tokens {
			result = append(result, hostJoinTokenResponse{
				ID: token.ID, Name: token.Name, ExpiresAt: token.ExpiresAtMillis, CreatedAt: token.CreatedAtMillis,
			})
		}
		writeJSON(response, http.StatusOK, map[string]any{"tokens": result})
	}
}

func createHostJoinToken(config handlerConfig) http.HandlerFunc {
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
		var body requestBody
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<16))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain a host name")
			return
		}
		_, _, correlationID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate identifiers")
			return
		}
		plaintext, token, err := config.hosts.CreateJoinToken(
			request.Context(), body.Name, identity.Subject, identity.Email, correlationID,
		)
		if err != nil {
			writeHostError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, hostJoinTokenResponse{
			ID: token.ID, Name: token.Name, Token: plaintext, Command: config.hosts.JoinCommand(plaintext),
			ExpiresAt: token.ExpiresAtMillis, CreatedAt: token.CreatedAtMillis,
		})
	}
}

func deleteHost(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		_, auditID, correlationID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate identifiers")
			return
		}
		err = config.hosts.DeleteHost(request.Context(), state.DeleteHostInput{
			ID: request.PathValue("hostID"), AuditEventID: auditID,
			ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, DeletedAtMillis: config.now().UnixMilli(),
		})
		if err != nil {
			writeHostError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func deleteHostJoinToken(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		_, auditID, correlationID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate identifiers")
			return
		}
		err = config.hosts.DeleteJoinToken(request.Context(), state.DeleteHostJoinTokenInput{
			ID: request.PathValue("tokenID"), AuditEventID: auditID,
			ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, DeletedAtMillis: config.now().UnixMilli(),
		})
		if err != nil {
			writeHostError(response, err)
			return
		}
		response.Header().Set("X-Request-ID", correlationID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func publicHost(host state.Host) hostResponse {
	return hostResponse{
		ID: host.ID, Name: host.Name, PublicIPv4: host.PublicIPv4,
		LastSeenAt: host.LastSeenMillis, JoinedAt: host.JoinedAtMillis,
		CreatedAt: host.CreatedAtMillis, UpdatedAt: host.UpdatedAtMillis,
	}
}

func writeHostError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrHostNotFound), errors.Is(err, state.ErrJoinTokenNotFound):
		writeAPIError(response, http.StatusNotFound, "host_not_found", err.Error())
	case errors.Is(err, state.ErrHostHasServices):
		writeAPIError(response, http.StatusConflict, "host_has_services", err.Error())
	case errors.Is(err, state.ErrJoinTokenConsumed), errors.Is(err, state.ErrHostNameConflict):
		writeAPIError(response, http.StatusConflict, "host_conflict", err.Error())
	default:
		writeAPIError(response, http.StatusBadRequest, "invalid_host", err.Error())
	}
}
