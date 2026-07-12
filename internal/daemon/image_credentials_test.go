package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestLiveImageCredentialRepositoryEncryptsAndResolvesAuthentication(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := liveImageCredentialRepository{store: store, master: master}
	created, err := repository.CreateImageCredential(context.Background(), server.CreateImageCredential{
		ID: "credential", ProjectID: "project", Name: "production", RegistryHost: "registry.example.com",
		Username: "robot", Password: "super-secret", AuditEventID: "audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(created.PasswordEncrypted, []byte("super-secret")) {
		t.Fatal("password was stored as plaintext")
	}
	credential, err := repository.Resolve(context.Background(), state.ServiceDesired{
		ProjectID: "project",
		Snapshot: serviceconfig.Snapshot{
			ImageReference: "registry.example.com/team/api:latest", ImageCredentialID: "credential",
		},
	})
	if err != nil || credential.Username != "robot" || credential.Password != "super-secret" {
		t.Fatalf("resolved credential = %+v, %v", credential, err)
	}
	if _, err := repository.Resolve(context.Background(), state.ServiceDesired{
		ProjectID: "project",
		Snapshot: serviceconfig.Snapshot{
			ImageReference: "other.example.com/team/api:latest", ImageCredentialID: "credential",
		},
	}); err == nil {
		t.Fatal("credential was accepted for another registry host")
	}
}
