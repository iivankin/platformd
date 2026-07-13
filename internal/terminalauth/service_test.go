package terminalauth_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/terminalauth"
)

func TestServiceIssuesSubjectBoundExpiringTokenAndClearsPassphrase(t *testing.T) {
	t.Parallel()
	verifier, err := passphrase.HashWith([]byte("correct horse battery staple"), bytes.NewReader(bytes.Repeat([]byte{0x2f}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_900_000_000, 0)
	service, err := terminalauth.New(terminalauth.Config{
		Master: testMaster(t), InstallationID: "installation", Verifier: verifier,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	value := []byte("correct horse battery staple")
	issued, err := service.Issue(context.Background(), "access-subject", value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value, make([]byte, len(value))) {
		t.Fatalf("passphrase was not cleared: %q", value)
	}
	if issued.ExpiresAt != now.Add(30*time.Second) || !strings.HasPrefix(issued.Value, "v1.") {
		t.Fatalf("issued token = %+v", issued)
	}
	if err := service.Verify(issued.Value, "access-subject"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "https://admin.example.com/api/v1/server/terminal", nil)
	request.Header.Set("Sec-WebSocket-Protocol", terminalauth.WebSocketProtocol+", "+terminalauth.WebSocketBearerPrefix+issued.Value)
	if err := service.VerifyWebSocketRequest(request, "access-subject"); err != nil {
		t.Fatalf("verify WebSocket bearer: %v", err)
	}
	request.Header.Set("Sec-WebSocket-Protocol", terminalauth.WebSocketBearerPrefix+issued.Value)
	if err := service.VerifyWebSocketRequest(request, "access-subject"); !errors.Is(err, terminalauth.ErrInvalidToken) {
		t.Fatalf("missing fixed WebSocket protocol verification = %v", err)
	}
	if err := service.Verify(issued.Value, "different-subject"); !errors.Is(err, terminalauth.ErrInvalidToken) {
		t.Fatalf("wrong-subject verification = %v", err)
	}
	replacement := byte('A')
	if issued.Value[len(issued.Value)-1] == replacement {
		replacement = 'B'
	}
	tampered := issued.Value[:len(issued.Value)-1] + string(replacement)
	if err := service.Verify(tampered, "access-subject"); !errors.Is(err, terminalauth.ErrInvalidToken) {
		t.Fatalf("tampered verification = %v", err)
	}
	now = issued.ExpiresAt
	if err := service.Verify(issued.Value, "access-subject"); !errors.Is(err, terminalauth.ErrInvalidToken) {
		t.Fatalf("expired verification = %v", err)
	}
}

func TestServiceDelaysFailuresAndUsesOnlyInMemoryGlobalCooldown(t *testing.T) {
	t.Parallel()
	verifier, err := passphrase.HashWith([]byte("correct"), bytes.NewReader(bytes.Repeat([]byte{0x39}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_900_000_000, 0)
	var delays []time.Duration
	service, err := terminalauth.New(terminalauth.Config{
		Master: testMaster(t), InstallationID: "installation", Verifier: verifier,
		Now: func() time.Time { return now },
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := service.Issue(context.Background(), "subject", []byte("wrong")); !errors.Is(err, terminalauth.ErrInvalidPassphrase) {
			t.Fatalf("failed verification = %v", err)
		}
	}
	if len(delays) != 5 {
		t.Fatalf("failure delays = %v", delays)
	}
	for _, delay := range delays {
		if delay != 2*time.Second {
			t.Fatalf("failure delay = %v", delay)
		}
	}
	if _, err := service.Issue(context.Background(), "subject", []byte("correct")); !errors.Is(err, terminalauth.ErrCooldown) {
		t.Fatalf("cooldown verification = %v", err)
	}
	now = now.Add(61 * time.Second)
	if _, err := service.Issue(context.Background(), "subject", []byte("correct")); err != nil {
		t.Fatalf("verification after cooldown = %v", err)
	}
}

func testMaster(t *testing.T) cryptobox.MasterKey {
	t.Helper()
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return master
}
