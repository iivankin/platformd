package hosttunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebsocketPeerPipesBytes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)

	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, acceptErr := websocket.Accept(response, request, &websocket.AcceptOptions{
			Subprotocols: []string{"platformd-host-tunnel-v1"},
		})
		if acceptErr != nil {
			return
		}
		peer := NewServerPeer(WrapWebsocket(connection), func(_ context.Context, hostname string, got uint16) (net.Conn, error) {
			if hostname != "db.shop.internal" || got != port {
				t.Fatalf("dialed %s:%d", hostname, got)
			}
			return net.Dial("tcp", listener.Addr().String())
		})
		_ = peer.Serve(request.Context())
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clientConn, _, err := websocket.Dial(ctx, "wss://"+strings.TrimPrefix(server.URL, "https://")+"/tunnel", &websocket.DialOptions{
		Subprotocols: []string{"platformd-host-tunnel-v1"},
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := NewPeer(WrapWebsocket(clientConn), nil)
	go func() { _ = client.Serve(ctx) }()

	conn, err := client.Dial(ctx, "db.shop.internal", port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	backend := <-accepted
	defer backend.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(backend, buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("backend read = %q %v", buffer, err)
	}
	if _, err := backend.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("client read = %q %v", buffer, err)
	}
}
