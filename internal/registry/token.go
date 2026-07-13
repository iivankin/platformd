package registry

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const RegistryTokenLifetime = 5 * time.Minute
const registryTokenKeyDomain = "platformd/registry/token/v1"
const maximumRegistryTokenBytes = 4096

var registryTokenHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

type tokenClaims struct {
	Audience     string   `json:"aud"`
	Issuer       string   `json:"iss"`
	RepositoryID string   `json:"rid"`
	Repository   string   `json:"repository"`
	CredentialID string   `json:"credentialId,omitempty"`
	Actions      []string `json:"actions,omitempty"`
	IssuedAt     int64    `json:"iat"`
	ExpiresAt    int64    `json:"exp"`
}

type tokenManager struct {
	key [32]byte
}

func newTokenManager(master cryptobox.MasterKey) (*tokenManager, error) {
	derived, err := hkdf.Key(
		sha256.New, master[:], []byte("platformd-registry-token"), registryTokenKeyDomain, 32,
	)
	if err != nil {
		return nil, err
	}
	manager := &tokenManager{}
	copy(manager.key[:], derived)
	clear(derived)
	return manager, nil
}

func (manager *tokenManager) issue(service, repositoryID, repository, credentialID string, actions []string, now time.Time) (string, error) {
	if service == "" || repositoryID == "" || repository == "" {
		return "", errors.New("registry token claims are incomplete")
	}
	claims := tokenClaims{
		Audience: service, Issuer: "platformd", RepositoryID: repositoryID,
		Repository: repository, CredentialID: credentialID,
		Actions: append([]string(nil), actions...), IssuedAt: now.Unix(),
		ExpiresAt: now.Add(RegistryTokenLifetime).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signed := registryTokenHeader + "." + encodedPayload
	signature := manager.signature(signed)
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (manager *tokenManager) verify(value, service string, now time.Time) (tokenClaims, error) {
	if value == "" || len(value) > maximumRegistryTokenBytes || strings.ContainsAny(value, " \t\r\n") {
		return tokenClaims{}, errors.New("registry token is malformed")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != registryTokenHeader {
		return tokenClaims{}, errors.New("registry token header is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return tokenClaims{}, errors.New("registry token signature is malformed")
	}
	expected := manager.signature(parts[0] + "." + parts[1])
	if subtle.ConstantTimeCompare(signature, expected) != 1 {
		return tokenClaims{}, errors.New("registry token signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, errors.New("registry token payload is malformed")
	}
	var claims tokenClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return tokenClaims{}, errors.New("registry token claims are invalid")
	}
	unix := now.Unix()
	if claims.Issuer != "platformd" || claims.Audience != service || claims.RepositoryID == "" || claims.Repository == "" ||
		claims.IssuedAt > unix+30 || claims.ExpiresAt <= unix || claims.ExpiresAt > claims.IssuedAt+int64(RegistryTokenLifetime/time.Second) {
		return tokenClaims{}, errors.New("registry token claims are expired or invalid")
	}
	for _, action := range claims.Actions {
		if action != "pull" && action != "push" {
			return tokenClaims{}, errors.New("registry token action is invalid")
		}
	}
	return claims, nil
}

func (manager *tokenManager) signature(value string) []byte {
	mac := hmac.New(sha256.New, manager.key[:])
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
