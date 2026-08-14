package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/state"
)

type logReaderStub struct {
	query containerlogs.Query
}

func (*logReaderStub) Download(context.Context, containerlogs.DownloadQuery, io.Writer) (containerlogs.DownloadResult, error) {
	return containerlogs.DownloadResult{}, nil
}

func (reader *logReaderStub) Read(_ context.Context, query containerlogs.Query) (containerlogs.Window, error) {
	reader.query = query
	return containerlogs.Window{Records: []containerlogs.Record{{Text: "GET /assets/logo.svg 200"}}}, nil
}

func TestResourceLogsValidateScopeAndUseTelemetryReader(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedRedis(ctx, state.CreateManagedRedis{
		ID: "cache", ProjectID: "project", Name: "cache", ImageTag: "7.4",
		ImageDigest: "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4",
		VolumeID:    "cache-volume", PasswordEncrypted: []byte("sealed"),
		AuditEventID: "redis-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}

	reader := &logReaderStub{}
	repository := liveLogRepository{store: store, telemetryReader: reader}
	window, err := repository.ResourceLogs(ctx, "project", containerlogs.ResourceQuery{
		Kind: "redis", ResourceID: "cache",
		Query: containerlogs.Query{DeploymentID: "cache", Contains: "ready", Limit: 20},
	})
	if err != nil || len(window.Records) != 1 || reader.query.ServiceID != "cache" || reader.query.Contains != "ready" {
		t.Fatalf("resource telemetry logs = %+v, query = %+v, error = %v", window, reader.query, err)
	}
	if _, err := repository.ResourceLogs(ctx, "another-project", containerlogs.ResourceQuery{
		Kind: "redis", ResourceID: "cache", Query: containerlogs.Query{Limit: 20},
	}); err == nil {
		t.Fatal("cross-project managed resource logs were accepted")
	}
	if _, err := repository.ResourceLogs(ctx, "project", containerlogs.ResourceQuery{
		Kind: "object_store", ResourceID: "objects", Query: containerlogs.Query{Limit: 20},
	}); !errors.Is(err, containerlogs.ErrInvalidQuery) {
		t.Fatalf("object store logs error = %v", err)
	}
}
