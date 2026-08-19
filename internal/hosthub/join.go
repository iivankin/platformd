package hosthub

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type joinRequest struct {
	Token      string `json:"token"`
	Name       string `json:"name"`
	PublicIPv4 string `json:"publicIpv4"`
}

type joinResponse struct {
	HostID         string `json:"hostId"`
	HostToken      string `json:"hostToken"`
	ParentHostname string `json:"parentHostname"`
	Name           string `json:"name"`
}

func (hub *Hub) JoinHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body joinRequest
		if err := decoder.Decode(&body); err != nil {
			http.Error(response, "Join request is invalid", http.StatusBadRequest)
			return
		}
		tokenID, secret, err := hosttoken.ParseJoin(body.Token)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		stored, err := hub.store.HostJoinTokenCredential(request.Context(), tokenID)
		if err != nil {
			writeJoinError(response, err)
			return
		}
		if !hosttoken.Verify("join", tokenID, secret, stored.TokenHMAC) {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hostID, err := id.New()
		if err != nil {
			http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		plaintext, hostSecret, err := hosttoken.GenerateHost(hostID, hub.random)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		auditID, err := id.New()
		if err != nil {
			http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		host, err := hub.store.ConsumeHostJoinToken(request.Context(), state.ConsumeHostJoinTokenInput{
			TokenID: tokenID, HostID: hostID, AuditEventID: auditID, Name: body.Name,
			PublicIPv4: body.PublicIPv4, TokenHMAC: hosttoken.Digest("host", hostID, hostSecret),
			ConsumedAtMillis: hub.now().UnixMilli(),
		})
		if err != nil {
			writeJoinError(response, err)
			return
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(response).Encode(joinResponse{
			HostID: host.ID, HostToken: plaintext, ParentHostname: hub.adminHostname, Name: host.Name,
		})
	})
}

func writeJoinError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrJoinTokenNotFound), errors.Is(err, state.ErrJoinTokenConsumed), errors.Is(err, state.ErrJoinTokenExpired):
		http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	case errors.Is(err, state.ErrInvalidPublicIPv4), errors.Is(err, state.ErrHostNameConflict):
		http.Error(response, err.Error(), http.StatusBadRequest)
	default:
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
