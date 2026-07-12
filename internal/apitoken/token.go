package apitoken

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const (
	prefix            = "ptk_"
	secretSize        = 32
	publicSize        = 36
	encodedSecretSize = 43
	tokenSize         = len(prefix) + publicSize + 1 + encodedSecretSize
)

type Verifier struct {
	key [sha256.Size]byte
}

func NewVerifier(master cryptobox.MasterKey) (Verifier, error) {
	derived, err := hkdf.Key(
		sha256.New, master[:], []byte("platformd"), "platformd/api-token-hmac/v1", sha256.Size,
	)
	if err != nil {
		return Verifier{}, fmt.Errorf("derive API token verifier key: %w", err)
	}
	var verifier Verifier
	copy(verifier.key[:], derived)
	clear(derived)
	return verifier, nil
}

func Generate(publicID string, random io.Reader) (string, string, error) {
	if len(publicID) != publicSize || strings.ContainsRune(publicID, '_') {
		return "", "", errors.New("API token public ID is invalid")
	}
	secretBytes := make([]byte, secretSize)
	if _, err := io.ReadFull(random, secretBytes); err != nil {
		return "", "", fmt.Errorf("generate API token secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	clear(secretBytes)
	return prefix + publicID + "_" + secret, secret, nil
}

func Parse(value string) (string, string, error) {
	if len(value) != tokenSize || !strings.HasPrefix(value, prefix) {
		return "", "", errors.New("API token prefix is invalid")
	}
	parts := strings.SplitN(strings.TrimPrefix(value, prefix), "_", 2)
	if len(parts) != 2 || len(parts[0]) != publicSize || len(parts[1]) != encodedSecretSize {
		return "", "", errors.New("API token format is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(decoded) != secretSize || base64.RawURLEncoding.EncodeToString(decoded) != parts[1] {
		clear(decoded)
		return "", "", errors.New("API token secret is invalid")
	}
	clear(decoded)
	return parts[0], parts[1], nil
}

func (verifier Verifier) Digest(publicID, secret string) []byte {
	digest := hmac.New(sha256.New, verifier.key[:])
	_, _ = digest.Write([]byte(publicID))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(secret))
	return digest.Sum(nil)
}

func (verifier Verifier) Verify(publicID, secret string, expected []byte) bool {
	actual := verifier.Digest(publicID, secret)
	valid := len(expected) == sha256.Size && hmac.Equal(actual, expected)
	clear(actual)
	return valid
}
