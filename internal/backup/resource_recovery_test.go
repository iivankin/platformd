package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestRestoreLatestResourceUsesNewestCompleteGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{1, 2, 3, 4}
	remote := newMemoryControlRemote()
	for index, payload := range [][]byte{[]byte("old"), []byte("new")} {
		generation := "generation-old"
		if index == 1 {
			generation = "generation-new"
		}
		built := resourcePublicationBuild(
			t, master, "redis", "cache", generation, payload, time.Unix(int64(10+index), 0),
		)
		if err := PublishResource(ctx, remote, master, built); err != nil {
			t.Fatal(err)
		}
		_ = os.RemoveAll(built.WorkDirectory)
	}
	var restored []byte
	completion, found, err := RestoreLatestResource(ctx, LatestResourceRestore{
		Remote: remote, Master: master, ResourceKind: "redis", ResourceID: "cache",
		Restorer: ResourceRestorerFunc(func(_ context.Context, request ResourceRestoreRequest) error {
			var readErr error
			restored, readErr = io.ReadAll(request.Source.Reader)
			return readErr
		}),
		Actor: Actor{Kind: "system", ID: "disaster_restore"},
	})
	if err != nil || !found || completion.GenerationID != "generation-new" {
		t.Fatalf("latest restore = %+v found=%v err=%v", completion, found, err)
	}
	if !bytes.Equal(restored, []byte("new")) {
		t.Fatalf("restored payload = %q", restored)
	}
}

func TestRestoreLatestResourceReportsMissingGenerationWithoutCallingRestorer(t *testing.T) {
	t.Parallel()
	called := false
	completion, found, err := RestoreLatestResource(context.Background(), LatestResourceRestore{
		Remote: newMemoryControlRemote(), Master: cryptobox.MasterKey{1},
		ResourceKind: "postgres", ResourceID: "database",
		Restorer: ResourceRestorerFunc(func(context.Context, ResourceRestoreRequest) error {
			called = true
			return nil
		}),
	})
	if err != nil || found || completion.GenerationID != "" || called {
		t.Fatalf("missing restore = %+v found=%v called=%v err=%v", completion, found, called, err)
	}
}

func TestRestoreLatestResourceRejectsPartialConsumption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{5, 6, 7, 8}
	remote := newMemoryControlRemote()
	built := resourcePublicationBuild(
		t, master, "registry", "repository", "generation", []byte("complete"), time.Unix(20, 0),
	)
	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(built.WorkDirectory)
	_, _, err := RestoreLatestResource(ctx, LatestResourceRestore{
		Remote: remote, Master: master, ResourceKind: "registry", ResourceID: "repository",
		Restorer: ResourceRestorerFunc(func(_ context.Context, request ResourceRestoreRequest) error {
			buffer := make([]byte, 1)
			_, readErr := request.Source.Reader.Read(buffer)
			return readErr
		}),
	})
	if err == nil {
		t.Fatal("partial recovery consumption was accepted")
	}
}
