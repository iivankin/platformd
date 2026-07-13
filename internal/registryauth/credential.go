package registryauth

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const usernamePrefix = "prg_"
const verifierDomain = "platformd/registry/credential/v1"

func Username(credentialID string) (string, error) {
	compact := strings.ReplaceAll(credentialID, "-", "")
	if len(compact) != 32 {
		return "", errors.New("registry credential ID must be a UUID")
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return "", errors.New("registry credential ID must be a UUID")
	}
	return usernamePrefix + compact, nil
}

func CredentialID(username string) (string, error) {
	compact := strings.TrimPrefix(username, usernamePrefix)
	if len(compact) != 32 || username == compact {
		return "", errors.New("registry username is invalid")
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return "", errors.New("registry username is invalid")
	}
	return compact[:8] + "-" + compact[8:12] + "-" + compact[12:16] + "-" + compact[16:20] + "-" + compact[20:], nil
}

func GenerateSecret(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("registry credential random source is nil")
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	defer clear(value)
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func Verifier(master cryptobox.MasterKey, repositoryID, credentialID, secret string) ([]byte, error) {
	if repositoryID == "" || credentialID == "" || secret == "" {
		return nil, errors.New("registry credential verifier input is incomplete")
	}
	key, err := hkdf.Key(sha256.New, master[:], []byte(repositoryID+"\x00"+credentialID), verifierDomain, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(secret))
	return mac.Sum(nil), nil
}

func Verify(master cryptobox.MasterKey, repositoryID, credentialID, secret string, expected []byte) bool {
	actual, err := Verifier(master, repositoryID, credentialID, secret)
	if err != nil || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
