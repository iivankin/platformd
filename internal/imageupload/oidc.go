package imageupload

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const (
	githubIssuer     = "https://token.actions.githubusercontent.com"
	githubJWKSURL    = githubIssuer + "/.well-known/jwks"
	maximumTokenSize = 16 << 10
	maximumJWKSSize  = 1 << 20
	keyTTL           = 6 * time.Hour
	clockSkew        = 60 * time.Second
)

var ErrOIDC = errors.New("invalid GitHub Actions OIDC token")

type OIDCVerifier struct {
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	keys      map[string]cachedOIDCKey
	refreshed time.Time
}

type cachedOIDCKey struct {
	key       *rsa.PublicKey
	expiresAt time.Time
}

type OIDCRequest struct {
	Token      string
	Audience   string
	Repository string
	Branch     string
	Workflows  []string
	Production bool
}

func NewOIDCVerifier(client *http.Client, now func() time.Time) *OIDCVerifier {
	if client == nil {
		client = &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if now == nil {
		now = time.Now
	}
	return &OIDCVerifier{client: client, now: now, keys: make(map[string]cachedOIDCKey)}
}

func (verifier *OIDCVerifier) Verify(ctx context.Context, request OIDCRequest) (state.ImageUploadIdentity, error) {
	header, claims, signingInput, signature, err := parseOIDCToken(request.Token)
	if err != nil || header.Algorithm != "RS256" || header.KeyID == "" || request.Audience == "" {
		return state.ImageUploadIdentity{}, ErrOIDC
	}
	key, err := verifier.key(ctx, header.KeyID)
	if err != nil {
		return state.ImageUploadIdentity{}, ErrOIDC
	}
	digest := sha256.Sum256([]byte(signingInput))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return state.ImageUploadIdentity{}, ErrOIDC
	}
	if err := verifier.validateClaims(claims, request); err != nil {
		return state.ImageUploadIdentity{}, ErrOIDC
	}
	return state.ImageUploadIdentity{
		Repository: strings.ToLower(claims.Repository), Ref: claims.Ref, SHA: claims.SHA,
		Workflow: claims.Workflow, WorkflowRef: claims.WorkflowRef, Actor: claims.Actor,
		RunID: claims.RunID, RunAttempt: claims.RunAttempt,
	}, nil
}

func (verifier *OIDCVerifier) validateClaims(claims oidcClaims, request OIDCRequest) error {
	now := verifier.now()
	if claims.Issuer != githubIssuer || !claims.Audience.Contains(request.Audience) ||
		strings.ToLower(claims.Repository) != strings.ToLower(request.Repository) ||
		claims.ExpiresAt == 0 || claims.IssuedAt == 0 || claims.Ref == "" || claims.SHA == "" ||
		claims.WorkflowRef == "" || claims.Actor == "" || claims.RunID == "" || claims.RunAttempt == "" {
		return ErrOIDC
	}
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(clockSkew)) ||
		now.Add(clockSkew).Before(time.Unix(claims.IssuedAt, 0)) ||
		time.Unix(claims.IssuedAt, 0).Before(now.Add(-15*time.Minute)) {
		return ErrOIDC
	}
	if claims.NotBefore != 0 && now.Add(clockSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return ErrOIDC
	}
	if request.Production && claims.Ref != "refs/heads/"+request.Branch {
		return ErrOIDC
	}
	if len(request.Workflows) != 0 {
		workflow := claims.WorkflowRef
		if at := strings.LastIndexByte(workflow, '@'); at >= 0 {
			workflow = workflow[:at]
		}
		workflow = path.Base(workflow)
		allowed := false
		for _, value := range request.Workflows {
			if workflow == value {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrOIDC
		}
	}
	return nil
}

func (verifier *OIDCVerifier) key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	now := verifier.now()
	verifier.mu.Lock()
	if value, ok := verifier.keys[keyID]; ok && now.Before(value.expiresAt) {
		verifier.mu.Unlock()
		return value.key, nil
	}
	needsRefresh := verifier.refreshed.IsZero() || now.Sub(verifier.refreshed) >= time.Minute
	verifier.mu.Unlock()
	if needsRefresh {
		keys, err := verifier.fetchKeys(ctx)
		if err != nil {
			return nil, err
		}
		verifier.mu.Lock()
		for id, key := range keys {
			verifier.keys[id] = cachedOIDCKey{key: key, expiresAt: now.Add(keyTTL)}
		}
		verifier.refreshed = now
		verifier.mu.Unlock()
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	value, ok := verifier.keys[keyID]
	if !ok || !now.Before(value.expiresAt) {
		return nil, ErrOIDC
	}
	return value.key, nil
}

func (verifier *OIDCVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, githubJWKSURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := verifier.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	expected, _ := url.Parse(githubJWKSURL)
	if response.StatusCode != http.StatusOK || response.Request == nil ||
		response.Request.URL.Scheme != expected.Scheme || response.Request.URL.Host != expected.Host {
		return nil, errors.New("unexpected GitHub OIDC JWKS response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumJWKSSize+1))
	if err != nil || len(data) > maximumJWKSSize {
		return nil, errors.New("invalid GitHub OIDC JWKS body")
	}
	var document struct {
		Keys []oidcJWK `json:"keys"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Keys) == 0 || len(document.Keys) > 64 {
		return nil, errors.New("invalid GitHub OIDC JWKS")
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, value := range document.Keys {
		if value.Type != "RSA" || value.Algorithm != "RS256" || value.KeyID == "" {
			continue
		}
		key, err := oidcRSAKey(value)
		if err != nil {
			return nil, err
		}
		if _, exists := keys[value.KeyID]; exists {
			return nil, errors.New("duplicate GitHub OIDC key")
		}
		keys[value.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("GitHub OIDC JWKS has no supported key")
	}
	return keys, nil
}

type oidcHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type oidcClaims struct {
	Audience    oidcAudience `json:"aud"`
	Issuer      string       `json:"iss"`
	ExpiresAt   int64        `json:"exp"`
	NotBefore   int64        `json:"nbf"`
	IssuedAt    int64        `json:"iat"`
	Repository  string       `json:"repository"`
	Ref         string       `json:"ref"`
	SHA         string       `json:"sha"`
	Workflow    string       `json:"workflow"`
	WorkflowRef string       `json:"workflow_ref"`
	Actor       string       `json:"actor"`
	RunID       string       `json:"run_id"`
	RunAttempt  string       `json:"run_attempt"`
}

type oidcAudience []string

func (values *oidcAudience) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*values = []string{one}
		return nil
	}
	return json.Unmarshal(data, (*[]string)(values))
}

func (values oidcAudience) Contains(expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func parseOIDCToken(token string) (oidcHeader, oidcClaims, string, []byte, error) {
	if token == "" || len(token) > maximumTokenSize {
		return oidcHeader{}, oidcClaims{}, "", nil, ErrOIDC
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return oidcHeader{}, oidcClaims{}, "", nil, ErrOIDC
	}
	decode := base64.RawURLEncoding.DecodeString
	headerData, err := decode(parts[0])
	if err != nil {
		return oidcHeader{}, oidcClaims{}, "", nil, err
	}
	claimsData, err := decode(parts[1])
	if err != nil {
		return oidcHeader{}, oidcClaims{}, "", nil, err
	}
	signature, err := decode(parts[2])
	if err != nil {
		return oidcHeader{}, oidcClaims{}, "", nil, err
	}
	var header oidcHeader
	var claims oidcClaims
	if json.Unmarshal(headerData, &header) != nil || json.Unmarshal(claimsData, &claims) != nil {
		return oidcHeader{}, oidcClaims{}, "", nil, ErrOIDC
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}

type oidcJWK struct {
	Algorithm string `json:"alg"`
	Exponent  string `json:"e"`
	KeyID     string `json:"kid"`
	Modulus   string `json:"n"`
	Type      string `json:"kty"`
}

func oidcRSAKey(value oidcJWK) (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(value.Modulus)
	if err != nil || len(modulus) < 256 || len(modulus) > 1024 {
		return nil, errors.New("invalid GitHub OIDC RSA modulus")
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(value.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, errors.New("invalid GitHub OIDC RSA exponent")
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	if exponent < 3 || exponent%2 == 0 {
		return nil, fmt.Errorf("invalid GitHub OIDC RSA exponent %d", exponent)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}, nil
}
