package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/terminalauth"
)

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
