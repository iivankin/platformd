package objectstore

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
)

const (
	secretBytes            = 32
	secretEncryptionDomain = "platformd/sqlite/s3-credential-secret/v1"
)

// InitialCredentials are generated with a UI draft and persisted unchanged
// when the draft is deployed.
type InitialCredentials struct {
	AccessKey string
	Secret    string
}

func AccessKeyID(credentialID string) (string, error) {
	if !id.Valid(credentialID) {
		return "", errors.New("S3 credential ID must be a CUID2")
	}
	return "ps3_" + credentialID, nil
}

func CredentialID(accessKeyID string) (string, error) {
	if !strings.HasPrefix(accessKeyID, "ps3_") {
		return "", errors.New("S3 access key ID is invalid")
	}
	credentialID := strings.TrimPrefix(accessKeyID, "ps3_")
	if !id.Valid(credentialID) {
		return "", errors.New("S3 access key ID is invalid")
	}
	return credentialID, nil
}

func GenerateSecret(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	value := make([]byte, secretBytes)
	defer clear(value)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func SealSecret(master cryptobox.MasterKey, storeID, credentialID, secret string) ([]byte, error) {
	if storeID == "" || credentialID == "" || !validSecret(secret) {
		return nil, errors.New("S3 credential secret input is invalid")
	}
	box, err := cryptobox.NewBox(master, []byte(storeID+":"+credentialID), secretEncryptionDomain)
	if err != nil {
		return nil, err
	}
	return box.Seal([]byte(secret), []byte(storeID+":"+credentialID+":secret"))
}

func OpenSecret(master cryptobox.MasterKey, storeID, credentialID string, encrypted []byte) (string, error) {
	box, err := cryptobox.NewBox(master, []byte(storeID+":"+credentialID), secretEncryptionDomain)
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(encrypted, []byte(storeID+":"+credentialID+":secret"))
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	if !validSecret(string(plaintext)) {
		return "", errors.New("decrypted S3 credential secret is invalid")
	}
	return string(plaintext), nil
}

func validSecret(value string) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(secretBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil
}
