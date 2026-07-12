package managedredis

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const (
	passwordBytes    = 32
	encryptionDomain = "platformd/sqlite/managed-redis-password/v1"
)

func GeneratePassword() (string, error) {
	return GeneratePasswordWith(rand.Reader)
}

func GeneratePasswordWith(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("password random source is required")
	}
	value := make([]byte, passwordBytes)
	defer clear(value)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func SealPassword(master cryptobox.MasterKey, resourceID, password string) ([]byte, error) {
	if resourceID == "" {
		return nil, errors.New("managed Redis resource ID is empty")
	}
	if password == "" || !utf8.ValidString(password) {
		return nil, errors.New("managed Redis password must be valid non-empty UTF-8")
	}
	box, err := cryptobox.NewBox(master, []byte(resourceID), encryptionDomain)
	if err != nil {
		return nil, err
	}
	return box.Seal([]byte(password), passwordAdditionalData(resourceID))
}

func OpenPassword(master cryptobox.MasterKey, resourceID string, encrypted []byte) (string, error) {
	if resourceID == "" {
		return "", errors.New("managed Redis resource ID is empty")
	}
	box, err := cryptobox.NewBox(master, []byte(resourceID), encryptionDomain)
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(encrypted, passwordAdditionalData(resourceID))
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	if len(plaintext) == 0 || !utf8.Valid(plaintext) {
		return "", errors.New("decrypted managed Redis password is invalid")
	}
	return string(plaintext), nil
}

func passwordAdditionalData(resourceID string) []byte {
	return []byte(resourceID + ":password")
}
