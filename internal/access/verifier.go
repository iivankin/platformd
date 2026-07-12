package access

import (
	"container/list"
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
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	keyTTL             = 6 * time.Hour
	refreshCooldown    = 30 * time.Second
	negativeTTL        = 30 * time.Second
	maximumNegativeIDs = 256
	maximumTokenBytes  = 16 << 10
	maximumJWKSBytes   = 1 << 20
	maximumJWKSKeys    = 64
	clockSkew          = 60 * time.Second
)

var (
	ErrInvalidToken = errors.New("invalid Cloudflare Access token")
	base64URL       = base64.RawURLEncoding
	teamDomain      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*\.cloudflareaccess\.com$`)
)

type Identity struct {
	Subject string
	Email   string
}

type Config struct {
	TeamDomain string
	Audience   string
	HTTPClient *http.Client
	Now        func() time.Time
}

type cachedKey struct {
	publicKey *rsa.PublicKey
	expiresAt time.Time
}

type negativeEntry struct {
	kid       string
	expiresAt time.Time
}

type Verifier struct {
	issuer     string
	audience   string
	jwksURL    string
	httpClient *http.Client
	now        func() time.Time

	mutex       sync.Mutex
	keys        map[string]cachedKey
	negative    map[string]*list.Element
	negativeLRU *list.List
	lastRefresh time.Time
	refreshing  bool
	refreshDone chan struct{}
}

func New(config Config) (*Verifier, error) {
	if !teamDomain.MatchString(config.TeamDomain) {
		return nil, errors.New("Access verifier requires a normalized <team>.cloudflareaccess.com domain")
	}
	if config.Audience == "" || len(config.Audience) > 512 {
		return nil, errors.New("Access verifier requires an audience containing 1..512 bytes")
	}
	issuer := "https://" + config.TeamDomain
	jwksURL := issuer + "/cdn-cgi/access/certs"
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		issuer:      issuer,
		audience:    config.Audience,
		jwksURL:     jwksURL,
		httpClient:  client,
		now:         now,
		keys:        make(map[string]cachedKey),
		negative:    make(map[string]*list.Element),
		negativeLRU: list.New(),
	}, nil
}

func (verifier *Verifier) Verify(ctx context.Context, token string) (Identity, error) {
	header, claims, signingInput, signature, err := parseToken(token)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	if header.Algorithm != "RS256" || header.KeyID == "" {
		return Identity{}, ErrInvalidToken
	}
	key, err := verifier.key(ctx, header.KeyID)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Identity{}, ErrInvalidToken
	}
	identity, err := verifier.validateClaims(claims)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	return identity, nil
}

func (verifier *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		now := verifier.now()
		verifier.mutex.Lock()
		verifier.expireNegative(now)
		if value, ok := verifier.keys[kid]; ok {
			if now.Before(value.expiresAt) {
				verifier.mutex.Unlock()
				return value.publicKey, nil
			}
			delete(verifier.keys, kid)
		}
		if _, ok := verifier.negative[kid]; ok {
			verifier.mutex.Unlock()
			return nil, ErrInvalidToken
		}
		if verifier.refreshing {
			done := verifier.refreshDone
			verifier.mutex.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if !verifier.lastRefresh.IsZero() && now.Sub(verifier.lastRefresh) < refreshCooldown {
			verifier.addNegative(kid, now)
			verifier.mutex.Unlock()
			return nil, ErrInvalidToken
		}
		verifier.refreshing = true
		verifier.refreshDone = make(chan struct{})
		verifier.lastRefresh = now
		verifier.mutex.Unlock()

		keys, refreshErr := verifier.fetchKeys(ctx)
		verifier.mutex.Lock()
		if refreshErr == nil {
			expiresAt := now.Add(keyTTL)
			for keyID, publicKey := range keys {
				verifier.keys[keyID] = cachedKey{publicKey: publicKey, expiresAt: expiresAt}
			}
		}
		if _, ok := verifier.keys[kid]; !ok {
			verifier.addNegative(kid, now)
		}
		close(verifier.refreshDone)
		verifier.refreshing = false
		verifier.mutex.Unlock()
		if refreshErr != nil {
			return nil, refreshErr
		}
	}
}

func (verifier *Verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, verifier.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create JWKS request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := verifier.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	expectedURL, _ := url.Parse(verifier.jwksURL)
	if response.Request == nil || response.Request.URL.Scheme != expectedURL.Scheme || response.Request.URL.Host != expectedURL.Host {
		return nil, errors.New("JWKS response came from an unexpected origin")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS: status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumJWKSBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read JWKS: %w", err)
	}
	if len(data) > maximumJWKSBytes {
		return nil, errors.New("JWKS exceeds 1 MiB")
	}
	var document jwksDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	if len(document.Keys) == 0 || len(document.Keys) > maximumJWKSKeys {
		return nil, errors.New("JWKS key count is outside bounds")
	}
	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, value := range document.Keys {
		if value.KeyType != "RSA" || value.Algorithm != "RS256" || value.Use != "sig" || value.KeyID == "" {
			continue
		}
		publicKey, err := rsaKey(value)
		if err != nil {
			return nil, err
		}
		if _, exists := keys[value.KeyID]; exists {
			return nil, errors.New("JWKS contains duplicate kid")
		}
		keys[value.KeyID] = publicKey
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS has no supported signing keys")
	}
	return keys, nil
}

func (verifier *Verifier) validateClaims(claims tokenClaims) (Identity, error) {
	now := verifier.now()
	if claims.Issuer != verifier.issuer || claims.Subject == "" || claims.Email == "" || !claims.Audience.Contains(verifier.audience) {
		return Identity{}, ErrInvalidToken
	}
	if claims.ExpiresAt == 0 || now.After(time.Unix(claims.ExpiresAt, 0).Add(clockSkew)) {
		return Identity{}, ErrInvalidToken
	}
	if claims.NotBefore != 0 && now.Add(clockSkew).Before(time.Unix(claims.NotBefore, 0)) {
		return Identity{}, ErrInvalidToken
	}
	return Identity{Subject: claims.Subject, Email: claims.Email}, nil
}

func (verifier *Verifier) expireNegative(now time.Time) {
	for element := verifier.negativeLRU.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(negativeEntry)
		if !now.Before(entry.expiresAt) {
			delete(verifier.negative, entry.kid)
			verifier.negativeLRU.Remove(element)
		}
		element = previous
	}
}

func (verifier *Verifier) addNegative(kid string, now time.Time) {
	if element, ok := verifier.negative[kid]; ok {
		verifier.negativeLRU.Remove(element)
	}
	element := verifier.negativeLRU.PushFront(negativeEntry{kid: kid, expiresAt: now.Add(negativeTTL)})
	verifier.negative[kid] = element
	for verifier.negativeLRU.Len() > maximumNegativeIDs {
		oldest := verifier.negativeLRU.Back()
		entry := oldest.Value.(negativeEntry)
		delete(verifier.negative, entry.kid)
		verifier.negativeLRU.Remove(oldest)
	}
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type tokenClaims struct {
	Audience  audience `json:"aud"`
	Email     string   `json:"email"`
	ExpiresAt int64    `json:"exp"`
	Issuer    string   `json:"iss"`
	NotBefore int64    `json:"nbf"`
	Subject   string   `json:"sub"`
}

type audience []string

func (values *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*values = []string{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return errors.New("aud must be a string or string array")
	}
	*values = multiple
	return nil
}

func (values audience) Contains(expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func parseToken(token string) (tokenHeader, tokenClaims, string, []byte, error) {
	if len(token) == 0 || len(token) > maximumTokenBytes {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	headerBytes, err := base64URL.DecodeString(parts[0])
	if err != nil {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	claimsBytes, err := base64URL.DecodeString(parts[1])
	if err != nil {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	signature, err := base64URL.DecodeString(parts[2])
	if err != nil {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	var header tokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	var claims tokenClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return tokenHeader{}, tokenClaims{}, "", nil, ErrInvalidToken
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Algorithm string `json:"alg"`
	Exponent  string `json:"e"`
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Modulus   string `json:"n"`
	Use       string `json:"use"`
}

func rsaKey(value jwk) (*rsa.PublicKey, error) {
	modulus, err := base64URL.DecodeString(value.Modulus)
	if err != nil || len(modulus) < 256 || len(modulus) > 1024 {
		return nil, errors.New("JWKS RSA modulus is invalid")
	}
	exponentBytes, err := base64URL.DecodeString(value.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, errors.New("JWKS RSA exponent is invalid")
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	if exponent < 3 || exponent%2 == 0 {
		return nil, errors.New("JWKS RSA exponent is invalid")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}, nil
}
