package hosttoken

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/id"
)

const (
	JoinPrefix        = "pjn_"
	HostPrefix        = "pht_"
	secretSize        = 32
	encodedSecretSize = 43
	joinTokenSize     = len(JoinPrefix) + id.Length + 1 + encodedSecretSize
	hostTokenSize     = len(HostPrefix) + id.Length + 1 + encodedSecretSize
)

func GenerateJoin(publicID string, random io.Reader) (string, string, error) {
	return generate(JoinPrefix, publicID, random)
}

func GenerateHost(publicID string, random io.Reader) (string, string, error) {
	return generate(HostPrefix, publicID, random)
}

func ParseJoin(value string) (string, string, error) {
	return parse(JoinPrefix, joinTokenSize, value)
}

func ParseHost(value string) (string, string, error) {
	return parse(HostPrefix, hostTokenSize, value)
}

func Digest(kind, publicID, secret string) []byte {
	sum := sha256.New()
	_, _ = io.WriteString(sum, kind)
	_, _ = sum.Write([]byte{0})
	_, _ = io.WriteString(sum, publicID)
	_, _ = sum.Write([]byte{0})
	_, _ = io.WriteString(sum, secret)
	return sum.Sum(nil)
}

func Verify(kind, publicID, secret string, expected []byte) bool {
	actual := Digest(kind, publicID, secret)
	valid := len(expected) == sha256.Size && hmacEqual(actual, expected)
	clear(actual)
	return valid
}

func generate(prefix, publicID string, random io.Reader) (string, string, error) {
	if !id.Valid(publicID) {
		return "", "", errors.New("host token public ID is invalid")
	}
	secretBytes := make([]byte, secretSize)
	if _, err := io.ReadFull(random, secretBytes); err != nil {
		return "", "", fmt.Errorf("generate host token secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	clear(secretBytes)
	return prefix + publicID + "_" + secret, secret, nil
}

func parse(prefix string, size int, value string) (string, string, error) {
	if len(value) != size || !strings.HasPrefix(value, prefix) {
		return "", "", errors.New("host token prefix is invalid")
	}
	parts := strings.SplitN(strings.TrimPrefix(value, prefix), "_", 2)
	if len(parts) != 2 || !id.Valid(parts[0]) || len(parts[1]) != encodedSecretSize {
		return "", "", errors.New("host token format is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(decoded) != secretSize || base64.RawURLEncoding.EncodeToString(decoded) != parts[1] {
		clear(decoded)
		return "", "", errors.New("host token secret is invalid")
	}
	clear(decoded)
	return parts[0], parts[1], nil
}

func hmacEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var acc byte
	for index := range left {
		acc |= left[index] ^ right[index]
	}
	return acc == 0
}
