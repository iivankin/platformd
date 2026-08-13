package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"time"
)

const artifactTokenPrefix = "ptel_artifact_"

type CredentialStore interface {
	ServiceArtifactTokenHash(context.Context, string) ([]byte, error)
	SetServiceArtifactTokenHash(context.Context, string, []byte, int64) error
}

type Credentials struct {
	store  CredentialStore
	random io.Reader
	now    func() time.Time
}

func NewCredentials(store CredentialStore, random io.Reader, now func() time.Time) (*Credentials, error) {
	if store == nil {
		return nil, errors.New("telemetry credential store is required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Credentials{store: store, random: random, now: now}, nil
}

func (credentials *Credentials) RotateArtifactToken(ctx context.Context, serviceID string) (string, error) {
	secret := make([]byte, 32)
	if _, err := io.ReadFull(credentials.random, secret); err != nil {
		return "", err
	}
	token := artifactTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	if err := credentials.store.SetServiceArtifactTokenHash(ctx, serviceID, hash[:], credentials.now().UnixMilli()); err != nil {
		return "", err
	}
	return token, nil
}

func (credentials *Credentials) VerifyArtifactToken(ctx context.Context, serviceID, token string) bool {
	if credentials == nil || serviceID == "" || len(token) <= len(artifactTokenPrefix) || token[:len(artifactTokenPrefix)] != artifactTokenPrefix {
		return false
	}
	expected, err := credentials.store.ServiceArtifactTokenHash(ctx, serviceID)
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], expected) == 1
}
