package registry

import (
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestRegistryTokenSignatureAudienceAndExpiry(t *testing.T) {
	t.Parallel()
	manager, err := newTokenManager(cryptobox.MasterKey{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_720_000_000, 0)
	token, err := manager.issue(
		"registry.example.com", "repository-id", "team/api", "credential-id",
		[]string{"pull", "push"}, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.verify(token, "registry.example.com", now.Add(time.Minute))
	if err != nil || claims.Repository != "team/api" || claims.CredentialID != "credential-id" || !tokenAllows(claims.Actions, true) {
		t.Fatalf("verified claims = %+v, %v", claims, err)
	}
	if _, err := manager.verify(token, "other.example.com", now); err == nil {
		t.Fatal("token was accepted for another Registry service")
	}
	if _, err := manager.verify(token, "registry.example.com", now.Add(RegistryTokenLifetime)); err == nil {
		t.Fatal("expired Registry token was accepted")
	}
	parts := strings.Split(token, ".")
	replacement := "A"
	if strings.HasSuffix(parts[1], replacement) {
		replacement = "B"
	}
	parts[1] = parts[1][:len(parts[1])-1] + replacement
	if _, err := manager.verify(strings.Join(parts, "."), "registry.example.com", now); err == nil {
		t.Fatal("tampered Registry token was accepted")
	}
}
