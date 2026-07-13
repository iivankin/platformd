package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/hostterminal"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/terminalauth"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

type testHostTerminal struct {
	opened  chan hostterminal.OpenInput
	session *testHostTerminalSession
}

func (terminal *testHostTerminal) Open(_ context.Context, input hostterminal.OpenInput) (terminaltransport.Session, error) {
	terminal.opened <- input
	return terminal.session, nil
}

type testHostTerminalSession struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	done   chan struct{}
	once   sync.Once
}

func newTestHostTerminalSession() *testHostTerminalSession {
	reader, writer := io.Pipe()
	return &testHostTerminalSession{reader: reader, writer: writer, done: make(chan struct{})}
}

func (session *testHostTerminalSession) Read(target []byte) (int, error) {
	return session.reader.Read(target)
}

func (session *testHostTerminalSession) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (session *testHostTerminalSession) Resize(terminaltransport.Size) error { return nil }

func (session *testHostTerminalSession) Wait() (int, error) {
	<-session.done
	return 0, nil
}

func (session *testHostTerminalSession) Close(string) error {
	session.once.Do(func() {
		close(session.done)
		_ = session.writer.Close()
		_ = session.reader.Close()
	})
	return nil
}

func TestServerTerminalTokenIsAccessOnlySubjectBoundAndNeverCached(t *testing.T) {
	verifier, err := passphrase.HashWith([]byte("correct"), bytes.NewReader(bytes.Repeat([]byte{0x51}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_900_000_000, 0)
	auth, err := terminalauth.New(terminalauth.Config{
		Master: master, InstallationID: "installation", Verifier: verifier,
		Now:   func() time.Time { return now },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithServerTerminalAuth(auth))
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	wrong := serverTerminalRequest(`{"passphrase":"wrong"}`)
	wrongResponse := httptest.NewRecorder()
	protected.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnauthorized || !strings.Contains(wrongResponse.Body.String(), "invalid_console_passphrase") {
		t.Fatalf("wrong passphrase = %d/%s", wrongResponse.Code, wrongResponse.Body)
	}

	correct := serverTerminalRequest(`{"passphrase":"correct"}`)
	correctResponse := httptest.NewRecorder()
	protected.ServeHTTP(correctResponse, correct)
	if correctResponse.Code != http.StatusOK || correctResponse.Header().Get("Cache-Control") != "no-store" ||
		correctResponse.Header().Get("Cloudflare-CDN-Cache-Control") != "no-store" {
		t.Fatalf("correct passphrase status/headers = %d/%v", correctResponse.Code, correctResponse.Header())
	}
	var issued struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(correctResponse.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt != now.Add(30*time.Second).UnixMilli() {
		t.Fatalf("expiry = %d", issued.ExpiresAt)
	}
	if err := auth.Verify(issued.Token, "subject"); err != nil {
		t.Fatalf("verify issued token: %v", err)
	}

	direct := serverTerminalRequest(`{"passphrase":"correct"}`)
	directResponse := httptest.NewRecorder()
	raw.ServeHTTP(directResponse, direct)
	if directResponse.Code != http.StatusForbidden {
		t.Fatalf("unprotected terminal token status = %d", directResponse.Code)
	}
	withoutOrigin := serverTerminalRequest(`{"passphrase":"correct"}`)
	withoutOrigin.Header.Del("Origin")
	withoutOriginResponse := httptest.NewRecorder()
	protected.ServeHTTP(withoutOriginResponse, withoutOrigin)
	if withoutOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("missing Origin status = %d", withoutOriginResponse.Code)
	}
}

func TestServerTerminalTokenRejectsNonJSONAndUnknownFields(t *testing.T) {
	verifier, err := passphrase.HashWith([]byte("correct"), bytes.NewReader(bytes.Repeat([]byte{0x52}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x73}, 32))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := terminalauth.New(terminalauth.Config{
		Master: master, InstallationID: "installation", Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServerTerminalAuth(auth)),
	)
	unknown := serverTerminalRequest(`{"passphrase":"correct","persist":true}`)
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", unknownResponse.Code)
	}
	nonJSON := serverTerminalRequest("correct")
	nonJSON.Header.Set("Content-Type", "text/plain")
	nonJSONResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonJSONResponse, nonJSON)
	if nonJSONResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON status = %d", nonJSONResponse.Code)
	}
}

func TestServerTerminalRequiresSubjectBoundBearerAndBlocksUpdate(t *testing.T) {
	verifier, err := passphrase.HashWith([]byte("correct"), bytes.NewReader(bytes.Repeat([]byte{0x53}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x74}, 32))
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := terminalauth.New(terminalauth.Config{
		Master: master, InstallationID: "installation", Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := authentication.Issue(context.Background(), "subject", []byte("correct"))
	if err != nil {
		t.Fatal(err)
	}
	session := newTestHostTerminalSession()
	application := &testHostTerminal{opened: make(chan hostterminal.OpenInput, 1), session: session}
	gate := admission.New()
	raw := server.Handler(
		server.DefaultMeta("ready"), server.WithAdmission(gate),
		server.WithServerTerminalAuth(authentication),
		server.WithServerTerminal("admin.example.com", application, time.Minute, time.Hour),
	)
	tlsServer := httptest.NewTLSServer(access.ProtectAdmin("admin.example.com", projectVerifier{}, raw))
	defer tlsServer.Close()

	options := serverTerminalDialOptions(tlsServer)
	options.Subprotocols = []string{
		terminalauth.WebSocketProtocol,
		terminalauth.WebSocketBearerPrefix + issued.Value,
	}
	connection, response, err := websocket.Dial(
		context.Background(), "wss://admin.example.com/api/v1/server/terminal?cols=120&rows=40", options,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	if connection.Subprotocol() != terminalauth.WebSocketProtocol {
		t.Fatalf("echoed subprotocol = %q", connection.Subprotocol())
	}
	opened := <-application.opened
	if opened.Actor.ID != "subject" || opened.Actor.Email != "admin@example.com" || opened.SourceIP != "203.0.113.8" ||
		opened.Size != (terminaltransport.Size{Cols: 120, Rows: 40}) {
		t.Fatalf("open input = %+v", opened)
	}
	if snapshot, updating := gate.Snapshot(); updating || snapshot.Total != 1 || snapshot.Blockers[0].Kind != "server_terminal" {
		t.Fatalf("active admission = %+v/%t", snapshot, updating)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, _ := gate.Snapshot()
		if snapshot.Total == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal admission remained active: %+v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}

	invalid := serverTerminalDialOptions(tlsServer)
	invalid.Subprotocols = []string{terminalauth.WebSocketProtocol, terminalauth.WebSocketBearerPrefix + "invalid"}
	_, response, err = websocket.Dial(
		context.Background(), "wss://admin.example.com/api/v1/server/terminal?cols=80&rows=24", invalid,
	)
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid bearer response = %#v, %v", response, err)
	}
}

func serverTerminalDialOptions(server *httptest.Server) *websocket.DialOptions {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test certificate is intentionally ephemeral.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		},
	}
	return &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
		HTTPHeader: http.Header{
			"Cf-Access-Jwt-Assertion": []string{"token"},
			"Cf-Connecting-Ip":        []string{"203.0.113.8"},
			"Origin":                  []string{"https://admin.example.com"},
		},
	}
}

func serverTerminalRequest(body string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost, "https://admin.example.com/api/v1/server/terminal-token", strings.NewReader(body),
	)
	request.Host = "admin.example.com"
	request.TLS = &tls.ConnectionState{ServerName: "admin.example.com"}
	request.RemoteAddr = "203.0.113.5:43210"
	request.Header.Set("Cf-Access-Jwt-Assertion", "token")
	request.Header.Set("Origin", "https://admin.example.com")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request
}
