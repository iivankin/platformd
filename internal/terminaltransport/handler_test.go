package terminaltransport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

type verifier struct{}

func (verifier) Verify(context.Context, string) (access.Identity, error) {
	return access.Identity{Subject: "subject", Email: "admin@example.com"}, nil
}

type fakeSession struct {
	outputReader *io.PipeReader
	outputWriter *io.PipeWriter

	mu      sync.Mutex
	input   bytes.Buffer
	resizes []terminaltransport.Size
	closed  bool
	reason  string
	done    chan struct{}
	once    sync.Once
}

func newFakeSession() *fakeSession {
	reader, writer := io.Pipe()
	return &fakeSession{outputReader: reader, outputWriter: writer, done: make(chan struct{})}
}

func (session *fakeSession) Read(target []byte) (int, error) {
	return session.outputReader.Read(target)
}

func (session *fakeSession) Write(payload []byte) (int, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.input.Write(payload)
}

func (session *fakeSession) Resize(size terminaltransport.Size) error {
	session.mu.Lock()
	session.resizes = append(session.resizes, size)
	session.mu.Unlock()
	return nil
}

func (session *fakeSession) Wait() (int, error) {
	<-session.done
	return 0, nil
}

func (session *fakeSession) Close(reason string) error {
	session.once.Do(func() {
		session.mu.Lock()
		session.closed = true
		session.reason = reason
		session.mu.Unlock()
		close(session.done)
		_ = session.outputWriter.Close()
		_ = session.outputReader.Close()
	})
	return nil
}

func TestTransportBridgesBinaryIOAndStrictResize(t *testing.T) {
	t.Parallel()

	session := newFakeSession()
	handler, err := terminaltransport.New("admin.example.com", func(_ context.Context, request terminaltransport.OpenRequest, size terminaltransport.Size) (terminaltransport.Session, error) {
		if request.Identity.Subject != "subject" {
			t.Fatalf("identity = %+v", request.Identity)
		}
		if size != (terminaltransport.Size{Cols: 120, Rows: 40}) {
			t.Fatalf("initial size = %+v", size)
		}
		go func() { _, _ = session.outputWriter.Write([]byte("ready\r\n")) }()
		return session, nil
	}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(access.ProtectAdmin("admin.example.com", verifier{}, handler))
	defer server.Close()

	connection, response, err := websocket.Dial(context.Background(), "wss://admin.example.com/terminal?cols=120&rows=40", dialOptions(server, "https://admin.example.com"))
	if err != nil {
		if response != nil {
			t.Fatalf("dial status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	messageType, payload, err := connection.Read(context.Background())
	if err != nil || messageType != websocket.MessageBinary || string(payload) != "ready\r\n" {
		t.Fatalf("output = %d %q, %v", messageType, payload, err)
	}
	if err := connection.Write(context.Background(), websocket.MessageBinary, []byte("whoami\r")); err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","cols":132,"rows":44}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.input.String() == "whoami\r" && len(session.resizes) == 1 && session.resizes[0] == (terminaltransport.Size{Cols: 132, Rows: 44})
	})
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	eventually(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.closed && session.reason == "client_closed"
	})
}

func TestTransportRejectsOriginAndInvalidDimensionsBeforeOpen(t *testing.T) {
	t.Parallel()

	var openCount atomic.Int32
	handler, err := terminaltransport.New("admin.example.com", func(context.Context, terminaltransport.OpenRequest, terminaltransport.Size) (terminaltransport.Session, error) {
		openCount.Add(1)
		return nil, errors.New("unexpected open")
	}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(access.ProtectAdmin("admin.example.com", verifier{}, handler))
	defer server.Close()

	_, response, err := websocket.Dial(context.Background(), "wss://admin.example.com/terminal?cols=80&rows=24", dialOptions(server, "https://evil.example.com"))
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin response = %#v, %v", response, err)
	}
	_, response, err = websocket.Dial(context.Background(), "wss://admin.example.com/terminal?cols=0&rows=24", dialOptions(server, "https://admin.example.com"))
	if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid size response = %#v, %v", response, err)
	}
	if openCount.Load() != 0 {
		t.Fatalf("opener called %d times", openCount.Load())
	}
}

func dialOptions(server *httptest.Server, origin string) *websocket.DialOptions {
	address := server.Listener.Addr().String()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test server certificate is intentionally ephemeral.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		},
	}
	return &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
		HTTPHeader: http.Header{
			"Origin":                  []string{origin},
			"Cf-Access-Jwt-Assertion": []string{"assertion"},
		},
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
