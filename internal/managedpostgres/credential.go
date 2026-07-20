package managedpostgres

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const (
	passwordBytes             = 32
	ownerEncryptionDomain     = "platformd/sqlite/managed-postgres-owner-password/v1"
	bootstrapEncryptionDomain = "platformd/sqlite/managed-postgres-bootstrap-password/v1"
)

type Credentials struct {
	DatabaseName      string
	OwnerUsername     string
	OwnerPassword     string
	BootstrapPassword string
}

// InitialCredentials are generated with a UI draft so references resolve
// before deployment. The bootstrap password remains internal to platformd.
type InitialCredentials struct {
	DatabaseName  string
	OwnerUsername string
	OwnerPassword string
}

func GenerateCredentials(resourceID string, random io.Reader) (Credentials, error) {
	if random == nil {
		random = rand.Reader
	}
	identifier := strings.ReplaceAll(resourceID, "-", "")
	if len(identifier) < 16 {
		return Credentials{}, errors.New("managed PostgreSQL resource ID is invalid")
	}
	ownerPassword, err := generatePassword(random)
	if err != nil {
		return Credentials{}, err
	}
	bootstrapPassword, err := generatePassword(random)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{
		DatabaseName: "app_" + identifier[:24], OwnerUsername: "owner_" + identifier[:24],
		OwnerPassword: ownerPassword, BootstrapPassword: bootstrapPassword,
	}, nil
}

func SealOwnerPassword(master cryptobox.MasterKey, resourceID, password string) ([]byte, error) {
	return sealPassword(master, resourceID, password, ownerEncryptionDomain, "owner")
}

func OpenOwnerPassword(master cryptobox.MasterKey, resourceID string, encrypted []byte) (string, error) {
	return openPassword(master, resourceID, encrypted, ownerEncryptionDomain, "owner")
}

func SealBootstrapPassword(master cryptobox.MasterKey, resourceID, password string) ([]byte, error) {
	return sealPassword(master, resourceID, password, bootstrapEncryptionDomain, "bootstrap")
}

func OpenBootstrapPassword(master cryptobox.MasterKey, resourceID string, encrypted []byte) (string, error) {
	return openPassword(master, resourceID, encrypted, bootstrapEncryptionDomain, "bootstrap")
}

func generatePassword(random io.Reader) (string, error) {
	value := make([]byte, passwordBytes)
	defer clear(value)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validateInitialCredentials(credentials InitialCredentials) error {
	if !validSQLIdentifier(credentials.DatabaseName) || !validSQLIdentifier(credentials.OwnerUsername) || !validPassword(credentials.OwnerPassword) {
		return errors.New("managed PostgreSQL initial credentials are invalid")
	}
	return nil
}

func validSQLIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func sealPassword(master cryptobox.MasterKey, resourceID, password, domain, kind string) ([]byte, error) {
	if resourceID == "" || !validPassword(password) {
		return nil, errors.New("managed PostgreSQL password input is invalid")
	}
	box, err := cryptobox.NewBox(master, []byte(resourceID), domain)
	if err != nil {
		return nil, err
	}
	return box.Seal([]byte(password), []byte(resourceID+":"+kind+":password"))
}

func openPassword(master cryptobox.MasterKey, resourceID string, encrypted []byte, domain, kind string) (string, error) {
	if resourceID == "" {
		return "", errors.New("managed PostgreSQL resource ID is empty")
	}
	box, err := cryptobox.NewBox(master, []byte(resourceID), domain)
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(encrypted, []byte(resourceID+":"+kind+":password"))
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	if !validPassword(string(plaintext)) {
		return "", errors.New("decrypted managed PostgreSQL password is invalid")
	}
	return string(plaintext), nil
}

func validPassword(value string) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(passwordBytes) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}
