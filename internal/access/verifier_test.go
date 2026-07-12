package access_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
)

func TestVerifyCachesKeyAndValidatesIdentity(t *testing.T) {
	t.Parallel()

	privateKey := testRSAKey(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeJWKS(t, response, "key-1", &privateKey.PublicKey)
	}))
	defer server.Close()
	now := time.Unix(1_800_000_000, 0)
	verifier := newVerifier(t, server.URL, func() time.Time { return now })
	token := signToken(t, privateKey, "key-1", map[string]any{
		"aud":   []string{"other", "audience"},
		"email": "admin@example.com",
		"exp":   now.Add(time.Hour).Unix(),
		"iss":   "https://team.cloudflareaccess.com",
		"nbf":   now.Add(-time.Minute).Unix(),
		"sub":   "subject-1",
	})

	for range 2 {
		identity, err := verifier.Verify(context.Background(), token)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Subject != "subject-1" || identity.Email != "admin@example.com" {
			t.Fatalf("identity = %+v", identity)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("JWKS requests = %d, want 1", requests.Load())
	}
}

func TestConcurrentUnknownKeyUsesOneRefreshAndCooldown(t *testing.T) {
	t.Parallel()

	privateKey := testRSAKey(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(25 * time.Millisecond)
		writeJWKS(t, response, "actual-key", &privateKey.PublicKey)
	}))
	defer server.Close()
	now := time.Unix(1_800_000_000, 0)
	verifier := newVerifier(t, server.URL, func() time.Time { return now })
	unknownToken := signToken(t, privateKey, "unknown-key", validClaims(now))

	var group sync.WaitGroup
	for range 24 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := verifier.Verify(context.Background(), unknownToken); err == nil {
				t.Error("unknown kid was accepted")
			}
		}()
	}
	group.Wait()
	if requests.Load() != 1 {
		t.Fatalf("concurrent JWKS requests = %d, want 1", requests.Load())
	}
	if _, err := verifier.Verify(context.Background(), signToken(t, privateKey, "another-unknown", validClaims(now))); err == nil {
		t.Fatal("second unknown kid was accepted during cooldown")
	}
	if requests.Load() != 1 {
		t.Fatalf("cooldown allowed another JWKS request: %d", requests.Load())
	}
}

func TestCachedKnownKeySurvivesFailedUnknownRefresh(t *testing.T) {
	t.Parallel()

	privateKey := testRSAKey(t)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(response, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJWKS(t, response, "key-1", &privateKey.PublicKey)
	}))
	defer server.Close()
	var nowUnix atomic.Int64
	nowUnix.Store(1_800_000_000)
	now := func() time.Time { return time.Unix(nowUnix.Load(), 0) }
	verifier := newVerifier(t, server.URL, now)
	known := signToken(t, privateKey, "key-1", validClaims(now()))
	if _, err := verifier.Verify(context.Background(), known); err != nil {
		t.Fatal(err)
	}
	nowUnix.Add(31)
	fail.Store(true)
	unknown := signToken(t, privateKey, "unknown", validClaims(now()))
	if _, err := verifier.Verify(context.Background(), unknown); err == nil {
		t.Fatal("unknown kid passed failed refresh")
	}
	if _, err := verifier.Verify(context.Background(), known); err != nil {
		t.Fatalf("cached known key stopped working: %v", err)
	}
}

func newVerifier(t *testing.T, jwksURL string, now func() time.Time) *access.Verifier {
	t.Helper()
	serverURL, err := url.Parse(jwksURL)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := access.New(access.Config{
		TeamDomain: "team.cloudflareaccess.com",
		Audience:   "audience",
		HTTPClient: &http.Client{Timeout: time.Second, Transport: rewriteTransport{target: serverURL}},
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

type rewriteTransport struct {
	target *url.URL
}

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	originalURL := request.URL
	rewritten := request.Clone(request.Context())
	copyURL := *request.URL
	copyURL.Scheme = transport.target.Scheme
	copyURL.Host = transport.target.Host
	rewritten.URL = &copyURL
	response, err := http.DefaultTransport.RoundTrip(rewritten)
	if err != nil {
		return nil, err
	}
	response.Request = request.Clone(request.Context())
	response.Request.URL = originalURL
	return response, nil
}

func validClaims(now time.Time) map[string]any {
	return map[string]any{
		"aud":   "audience",
		"email": "admin@example.com",
		"exp":   now.Add(time.Hour).Unix(),
		"iss":   "https://team.cloudflareaccess.com",
		"sub":   "subject-1",
	}
}

func signToken(t *testing.T, privateKey *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	signingInput := encoding.EncodeToString(headerJSON) + "." + encoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + encoding.EncodeToString(signature)
}

func writeJWKS(t *testing.T, response http.ResponseWriter, kid string, publicKey *rsa.PublicKey) {
	t.Helper()
	exponent := bigEndianBytes(publicKey.E)
	document := map[string]any{"keys": []map[string]string{{
		"alg": "RS256",
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
		"kid": kid,
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"use": "sig",
	}}}
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(document); err != nil {
		t.Errorf("encode JWKS: %v", err)
	}
}

func bigEndianBytes(value int) []byte {
	return big.NewInt(int64(value)).Bytes()
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}
