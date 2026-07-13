package objectstore

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const (
	secretBytes            = 32
	secretEncryptionDomain = "platformd/sqlite/s3-credential-secret/v1"
)

func AccessKeyID(credentialID string) (string, error) {
	compact := strings.ReplaceAll(credentialID, "-", "")
	if len(compact) != 32 {
		return "", errors.New("S3 credential ID must be a UUID")
	}
	return "ps3_" + compact, nil
}

func CredentialID(accessKeyID string) (string, error) {
	if len(accessKeyID) != 36 || !strings.HasPrefix(accessKeyID, "ps3_") {
		return "", errors.New("S3 access key ID is invalid")
	}
	value := accessKeyID[4:]
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return "", errors.New("S3 access key ID is invalid")
		}
	}
	return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:], nil
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
