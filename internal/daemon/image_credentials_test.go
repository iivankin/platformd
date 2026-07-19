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
	created, err := repository.PrepareServiceImageCredential(context.Background(), server.ServiceImageCredentialInput{
		ServiceID: "service", ImageReference: "registry.example.com/team/api:latest",
		Username: "robot", Password: "super-secret", UpdatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(created.PasswordEncrypted, []byte("super-secret")) {
		t.Fatal("password was stored as plaintext")
	}
	service, err := store.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: false,
		Snapshot:        serviceconfig.Snapshot{Source: serviceconfig.PrivateImageSource("registry.example.com/team/api:latest")},
		ImageCredential: created, AuditEventID: "audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := repository.Resolve(context.Background(), state.ServiceDesired{
		ID: service.ID, ProjectID: "project",
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PrivateImageSource("registry.example.com/team/api:latest"),
		},
	})
	if err != nil || credential.Username != "robot" || credential.Password != "super-secret" {
		t.Fatalf("resolved credential = %+v, %v", credential, err)
	}
	host, username, password, err := repository.RevealServiceImageCredential(context.Background(), service.ID)
	if err != nil || host != "registry.example.com" || username != "robot" || password != "super-secret" {
		t.Fatalf("revealed credential = %q/%q/%q, %v", host, username, password, err)
	}
	if _, err := repository.Resolve(context.Background(), state.ServiceDesired{
		ID: service.ID, ProjectID: "project",
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PrivateImageSource("other.example.com/team/api:latest"),
		},
	}); err == nil {
		t.Fatal("credential was accepted for another registry host")
	}
}
