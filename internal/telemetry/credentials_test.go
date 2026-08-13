package telemetry

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

type memoryCredentialStore struct {
	hash      []byte
	serviceID string
	updatedAt int64
}

func (store *memoryCredentialStore) ServiceArtifactTokenHash(_ context.Context, serviceID string) ([]byte, error) {
	if serviceID != store.serviceID || len(store.hash) == 0 {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), store.hash...), nil
}

func (store *memoryCredentialStore) SetServiceArtifactTokenHash(_ context.Context, serviceID string, hash []byte, updatedAt int64) error {
	store.serviceID = serviceID
	store.hash = append([]byte(nil), hash...)
	store.updatedAt = updatedAt
	return nil
}

func TestArtifactTokenRotationInvalidatesPreviousToken(t *testing.T) {
	t.Parallel()
	store := &memoryCredentialStore{}
	random := bytes.NewReader(append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...))
	credentials, err := NewCredentials(store, random, func() time.Time { return time.UnixMilli(42) })
	if err != nil {
		t.Fatal(err)
	}
	first, err := credentials.RotateArtifactToken(context.Background(), "service")
	if err != nil || !credentials.VerifyArtifactToken(context.Background(), "service", first) {
		t.Fatalf("first token was not accepted: %v", err)
	}
	second, err := credentials.RotateArtifactToken(context.Background(), "service")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || credentials.VerifyArtifactToken(context.Background(), "service", first) || !credentials.VerifyArtifactToken(context.Background(), "service", second) {
		t.Fatal("artifact token rotation did not replace the verifier")
	}
	if store.updatedAt != 42 {
		t.Fatalf("updatedAt = %d", store.updatedAt)
	}
}
