package hosthub

import (
	"context"
	"log"
	"net/http"

	"github.com/coder/websocket"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/hosttunnel"
)

func (hub *Hub) TunnelHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !offersProtocol(request.Header.Values("Sec-WebSocket-Protocol"), hostconn.WebSocketTunnelProtocol) {
			http.Error(response, "Required WebSocket protocol is missing", http.StatusBadRequest)
			return
		}
		token, ok := bearerHostToken(request.Header.Values("Authorization"))
		if !ok {
			response.Header().Set("WWW-Authenticate", `Bearer realm="platformd-host"`)
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hostID, secret, err := hosttoken.ParseHost(token)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		stored, err := hub.store.HostCredential(request.Context(), hostID)
		if err != nil || !hosttoken.Verify("host", hostID, secret, stored.TokenHMAC) {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hub.mu.Lock()
		current := hub.sessions[hostID]
		hub.mu.Unlock()
		if current == nil {
			http.Error(response, "Child control session is not connected", http.StatusConflict)
			return
		}
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
			Subprotocols: []string{hostconn.WebSocketTunnelProtocol},
		})
		if err != nil {
			return
		}
		peer := hosttunnel.NewPeer(hosttunnel.WrapWebsocket(connection), hub.dialLocal)
		hub.mu.Lock()
		if hub.sessions[hostID] != current {
			hub.mu.Unlock()
			_ = peer.Close()
			return
		}
		previous := current.tunnel
		current.tunnel = peer
		hub.mu.Unlock()
		if previous != nil {
			_ = previous.Close()
		}
		if err := peer.Serve(context.Background()); err != nil {
			log.Printf("host %s tunnel: %v", hostID, err)
		}
		hub.mu.Lock()
		if current.tunnel == peer {
			current.tunnel = nil
		}
		hub.mu.Unlock()
	})
}
