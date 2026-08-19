package hosthub

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/state"
)

func (hub *Hub) ConnectHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !offersProtocol(request.Header.Values("Sec-WebSocket-Protocol"), hostconn.WebSocketProtocol) {
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
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
			Subprotocols: []string{hostconn.WebSocketProtocol},
		})
		if err != nil {
			return
		}
		connection.SetReadLimit(maximumOTLPBytes)
		ctx, cancel := context.WithCancel(context.Background())
		current := hub.attach(hostID, cancel)
		defer func() {
			hub.detach(current)
			cancel()
			_ = connection.Close(websocket.StatusNormalClosure, "")
		}()
		welcome, err := hub.welcomePayload(ctx, hostID)
		if err != nil {
			log.Printf("host %s welcome: %v", hostID, err)
			return
		}
		if err := wsjson.Write(ctx, connection, encodeEnvelope(hostconn.KindWelcome, welcome)); err != nil {
			return
		}
		go current.writeLoop(ctx, connection)
		current.readLoop(ctx, connection)
	})
}

func (current *session) writeLoop(ctx context.Context, connection *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case envelope := <-current.writes:
			if err := wsjson.Write(ctx, connection, envelope); err != nil {
				current.cancel()
				return
			}
		}
	}
}

func (current *session) readLoop(ctx context.Context, connection *websocket.Conn) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, sessionIdleAfter)
		var envelope hostconn.Envelope
		err := wsjson.Read(readCtx, connection, &envelope)
		cancel()
		if err != nil {
			return
		}
		if err := current.hub.handle(ctx, current.hostID, envelope); err != nil {
			log.Printf("host %s %s: %v", current.hostID, envelope.Kind, err)
			if envelope.ID != "" {
				select {
				case current.writes <- hostconn.Envelope{ID: envelope.ID, Kind: resultKind(envelope.Kind), Error: err.Error()}:
				default:
				}
			}
		}
	}
}

func (hub *Hub) handle(ctx context.Context, hostID string, envelope hostconn.Envelope) error {
	switch envelope.Kind {
	case hostconn.KindHello:
		var hello hostconn.Hello
		if err := json.Unmarshal(envelope.Payload, &hello); err != nil {
			return err
		}
		return hub.observe(ctx, hostID, hello.PublicIPv4)
	case hostconn.KindStatus:
		var status hostconn.Status
		if err := json.Unmarshal(envelope.Payload, &status); err != nil {
			return err
		}
		hub.recordServices(hostID, status.Services)
		return hub.observe(ctx, hostID, status.PublicIPv4)
	case hostconn.KindOTLP:
		var batch hostconn.OTLPBatch
		if err := json.Unmarshal(envelope.Payload, &batch); err != nil {
			return err
		}
		return hub.ingestOTLP(ctx, batch)
	case hostconn.KindRPC:
		return hub.handleRPC(ctx, hostID, envelope)
	case hostconn.KindPurgeCache:
		return hub.handlePurge(ctx, hostID, envelope)
	default:
		return errors.New("unsupported host frame")
	}
}

func (hub *Hub) recordServices(hostID string, reports []hostconn.ServiceRuntime) {
	next := make(map[string]serviceReport, len(reports))
	for _, item := range reports {
		if item.ServiceID == "" {
			continue
		}
		switch item.Status {
		case "pending", "running", "failed", "degraded", "disabled":
			next[item.ServiceID] = serviceReport{status: item.Status, message: item.Message}
		}
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current := hub.sessions[hostID]
	if current == nil {
		return
	}
	current.services = next
}

func (hub *Hub) observe(ctx context.Context, hostID, publicIPv4 string) error {
	previous, err := hub.store.Host(ctx, hostID)
	if err != nil {
		return err
	}
	now := hub.now().UnixMilli()
	if err := hub.store.UpdateHostRuntime(ctx, state.UpdateHostRuntimeInput{
		ID: hostID, PublicIPv4: publicIPv4, LastSeenMillis: now, UpdatedAtMillis: now,
	}); err != nil {
		return err
	}
	if publicIPv4 != "" && publicIPv4 != previous.PublicIPv4 && hub.onAddress != nil {
		hub.onAddress(hostID)
	}
	return nil
}

func (hub *Hub) handlePurge(ctx context.Context, hostID string, envelope hostconn.Envelope) error {
	var request hostconn.PurgeCache
	if err := json.Unmarshal(envelope.Payload, &request); err != nil {
		return err
	}
	result := hostconn.Envelope{ID: envelope.ID, Kind: hostconn.KindPurgeResult}
	if hub.cloudflare == nil {
		result.Error = "Cloudflare DNS is not configured"
	} else if err := hub.cloudflare.PurgeHostnames(ctx, request.Hostnames); err != nil {
		result.Error = err.Error()
	}
	return hub.reply(ctx, hostID, result)
}

func (hub *Hub) reply(ctx context.Context, hostID string, envelope hostconn.Envelope) error {
	hub.mu.Lock()
	current := hub.sessions[hostID]
	hub.mu.Unlock()
	if current == nil {
		return ErrHostOffline
	}
	select {
	case current.writes <- envelope:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func encodeEnvelope(kind string, payload any) hostconn.Envelope {
	body, _ := json.Marshal(payload)
	return hostconn.Envelope{Kind: kind, Payload: body}
}

func resultKind(kind string) string {
	if kind == hostconn.KindRPC {
		return hostconn.KindRPCResult
	}
	if kind == hostconn.KindPurgeCache {
		return hostconn.KindPurgeResult
	}
	return kind
}

func bearerHostToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], hosttoken.HostPrefix) {
		return "", false
	}
	return parts[1], true
}

func offersProtocol(values []string, expected string) bool {
	for _, value := range values {
		for _, protocol := range strings.Split(value, ",") {
			if strings.TrimSpace(protocol) == expected {
				return true
			}
		}
	}
	return false
}
