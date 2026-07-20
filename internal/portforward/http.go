package portforward

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const maximumSessionLifetime = 8 * time.Hour

type tcpDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type HandlerConfig struct {
	Application *Application
	Dialer      tcpDialer
}

func Handler(config HandlerConfig) (http.Handler, error) {
	if config.Application == nil {
		return nil, errors.New("port forward handler application is missing")
	}
	if config.Dialer == nil {
		config.Dialer = &net.Dialer{Timeout: 5 * time.Second}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !offersProtocol(request.Header.Values("Sec-WebSocket-Protocol"), WebSocketProtocol) {
			http.Error(response, "Required WebSocket protocol is missing", http.StatusBadRequest)
			return
		}
		ticket, ok := bearerTicket(request.Header.Values("Authorization"))
		if !ok {
			response.Header().Set("WWW-Authenticate", `Bearer realm="platformd-port-forward"`)
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		session, err := config.Application.Acquire(ticket)
		if err != nil {
			writeSessionError(response, err)
			return
		}
		defer session.Release()
		target, err := config.Dialer.DialContext(request.Context(), "tcp", session.Target)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		defer target.Close()
		connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
			Subprotocols: []string{WebSocketProtocol},
		})
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), maximumSessionLifetime)
		defer cancel()
		stream := websocket.NetConn(ctx, connection, websocket.MessageBinary)
		bridge(stream, target)
	}), nil
}

func bridge(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyStream := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyStream(left, right)
	go copyStream(right, left)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func bearerTicket(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "pft_") {
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

func writeSessionError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidTicket):
		response.Header().Set("WWW-Authenticate", `Bearer realm="platformd-port-forward"`)
		http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	case errors.Is(err, ErrConnectionLimit):
		http.Error(response, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	default:
		http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
	}
}
