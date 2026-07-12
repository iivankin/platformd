package imagecredential

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/cryptobox"
	"go.podman.io/image/v5/docker/reference"
)

const (
	encryptionDomain     = "platformd/sqlite/image-registry-credential/v1"
	maximumUsernameBytes = 1024
	maximumPasswordBytes = 64 << 10
)

func NormalizeHost(value string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(value))
	if host == "" || strings.ContainsAny(host, "/@ ") {
		return "", errors.New("registry host must be host[:port] without scheme or path")
	}
	named, err := reference.ParseDockerRef(host + "/platformd-probe:latest")
	if err != nil {
		return "", fmt.Errorf("invalid registry host: %w", err)
	}
	normalized := reference.Domain(named)
	if normalized != host {
		return "", fmt.Errorf("registry host must use canonical form %q", normalized)
	}
	return normalized, nil
}

func HostForReference(value string) (string, error) {
	named, err := reference.ParseDockerRef(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid image reference: %w", err)
	}
	return reference.Domain(named), nil
}

func ValidateAuthentication(username, password string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	return ValidatePassword(password)
}

func ValidateUsername(username string) error {
	if username == "" || len(username) > maximumUsernameBytes || !utf8.ValidString(username) || strings.ContainsAny(username, ":\r\n\x00") {
		return errors.New("registry username must be valid UTF-8 without colon, control separators, or NUL")
	}
	return nil
}

func ValidatePassword(password string) error {
	if password == "" || len(password) > maximumPasswordBytes || !utf8.ValidString(password) || strings.ContainsRune(password, '\x00') {
		return errors.New("registry password must be valid non-empty UTF-8 without NUL and at most 64 KiB")
	}
	return nil
}

func SealPassword(master cryptobox.MasterKey, credentialID, password string) ([]byte, error) {
	if credentialID == "" {
		return nil, errors.New("image credential ID is empty")
	}
	box, err := cryptobox.NewBox(master, []byte(credentialID), encryptionDomain)
	if err != nil {
		return nil, err
	}
	return box.Seal([]byte(password), passwordAdditionalData(credentialID))
}

func OpenPassword(master cryptobox.MasterKey, credentialID string, encrypted []byte) (string, error) {
	if credentialID == "" {
		return "", errors.New("image credential ID is empty")
	}
	box, err := cryptobox.NewBox(master, []byte(credentialID), encryptionDomain)
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(encrypted, passwordAdditionalData(credentialID))
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	if !utf8.Valid(plaintext) {
		return "", errors.New("decrypted registry password is not UTF-8")
	}
	return string(plaintext), nil
}

func passwordAdditionalData(credentialID string) []byte {
	return []byte(credentialID + ":password")
}
