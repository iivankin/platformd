package portforward

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/automation"
)

func TestWebSocketForwardsRawTCPWithoutTicketInURL(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		connection, acceptErr := echo.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()

	resolver := &resolverStub{address: echo.Addr().String()}
	application, err := New(Config{
		Repository: resourceRepositoryStub{}, Resolver: resolver, Audit: &auditStub{},
		NewID: func() (string, error) { return "ticket-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := application.Create(context.Background(), automation.Identity{TokenID: "admin", Role: "admin"}, CreateInput{
		ProjectID: "project", ResourceKind: "service", ResourceID: "api", Port: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(HandlerConfig{Application: application})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	endpoint := "wss" + strings.TrimPrefix(server.URL, "https") + EndpointPath

	_, response, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{
		HTTPClient: server.Client(), Subprotocols: []string{WebSocketProtocol},
	})
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized handshake = %v / %+v", err, response)
	}

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+grant.Ticket)
	connection, _, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{
		HTTPClient: server.Client(), HTTPHeader: header, Subprotocols: []string{WebSocketProtocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := websocket.NetConn(context.Background(), connection, websocket.MessageBinary)
	defer stream.Close()
	if _, err := stream.Write([]byte("postgres-wire-data")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("postgres-wire-data"))
	if _, err := io.ReadFull(stream, buffer); err != nil || string(buffer) != "postgres-wire-data" {
		t.Fatalf("forwarded bytes = %q / %v", buffer, err)
	}
}
