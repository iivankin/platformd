package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestObjectStoreLogsUseScopedAuditActivity(t *testing.T) {
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
	if _, _, err := store.CreateObjectStore(context.Background(), state.CreateObjectStore{
		ID: "objects", ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		CredentialID: "credential", CredentialName: "deployer", CredentialPermission: "read_write", CredentialSecret: []byte("sealed"),
		AuditEventID: "object-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}

	repository := liveLogRepository{store: store}
	window, err := repository.ResourceLogs(context.Background(), "project", "object_store", "objects", "", "create", 20)
	if err != nil || len(window.Records) != 1 || window.Records[0].Text != "object_store.create succeeded" || window.Records[0].DeploymentID != "objects" {
		t.Fatalf("object storage logs = %+v, %v", window, err)
	}
	if _, err := repository.ResourceLogs(context.Background(), "another-project", "object_store", "objects", "", "", 20); !errors.Is(err, state.ErrObjectStoreNotFound) {
		t.Fatalf("cross-project logs error = %v", err)
	}
}
