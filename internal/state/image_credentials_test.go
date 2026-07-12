package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImageRegistryCredentialIsProjectScopedAndAudited(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.database.Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES ('project', 'shop', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateImageRegistryCredential(context.Background(), CreateImageRegistryCredential{
		ImageRegistryCredential: ImageRegistryCredential{
			ID: "credential", ProjectID: "project", Name: "production", RegistryHost: "REGISTRY.EXAMPLE.COM:5443",
			Username: "robot", PasswordEncrypted: []byte("ciphertext"), CreatedAtMillis: 2,
		},
		AuditEventID: "audit", ActorID: "actor", ActorEmail: "admin@example.com", RequestCorrelationID: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RegistryHost != "registry.example.com:5443" {
		t.Fatalf("registry host = %q", created.RegistryHost)
	}
	loaded, err := store.ImageRegistryCredential(context.Background(), "credential")
	if err != nil || string(loaded.PasswordEncrypted) != "ciphertext" {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
	_, err = store.CreateImageRegistryCredential(context.Background(), CreateImageRegistryCredential{
		ImageRegistryCredential: ImageRegistryCredential{
			ID: "credential-2", ProjectID: "project", Name: "production", RegistryHost: "registry.example.com",
			Username: "robot", PasswordEncrypted: []byte("ciphertext"), CreatedAtMillis: 3,
		},
		AuditEventID: "audit-2", ActorID: "actor", ActorEmail: "admin@example.com",
	})
	if !errors.Is(err, ErrImageCredentialNameConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
}
