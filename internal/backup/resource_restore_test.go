package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

func TestResourceRestoreServiceReturnsRunningOperationAndFinishesAsynchronously(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	remote := newMemoryControlRemote()
	payload := []byte("redis restore payload")
	built := resourcePublicationBuild(
		t, master, "redis", "redis-1", "generation-1", payload, time.Unix(40, 0),
	)
	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(built.WorkDirectory)
	restored := make(chan []byte, 1)
	succeeded := make(chan ResourceRestoreRequest, 1)
	service, err := NewResourceRestoreService(ResourceRestoreServiceConfig{
		Context: ctx, Store: store, Target: target, TargetGate: targetGate,
		Admission: admission.New(), Master: master,
		Restorers: map[string]ResourceRestorer{
			"redis": ResourceRestorerFunc(func(_ context.Context, request ResourceRestoreRequest) error {
				value, err := io.ReadAll(request.Source.Reader)
				if err == nil {
					restored <- value
				}
				return err
			}),
		},
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return remote, nil },
		Now:           func() time.Time { return time.Unix(50, 0) },
		OnSuccess:     func(request ResourceRestoreRequest) { succeeded <- request },
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.Start(
		ctx, "redis", "redis-1", "generation-1", ResourceRestoreOptions{},
		Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	)
	if err != nil || operation.Status != "running" || operation.ID == "" {
		t.Fatalf("started operation = %+v, %v", operation, err)
	}
	select {
	case value := <-restored:
		if !bytes.Equal(value, payload) {
			t.Fatalf("restored payload = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("resource restorer did not run")
	}
	finished := waitForOperation(t, store, operation.ID)
	if finished.Status != "succeeded" || finished.Progress != "complete" {
		t.Fatalf("finished operation = %+v", finished)
	}
	select {
	case request := <-succeeded:
		if request.GenerationID != "generation-1" || request.ResourceID != "redis-1" {
			t.Fatalf("success callback request = %+v", request)
		}
	default:
		t.Fatal("successful restore callback was not called")
	}
}

func TestResourceRestoreServiceFailsIfRestorerDoesNotConsumeGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	remote := newMemoryControlRemote()
	built := resourcePublicationBuild(
		t, master, "postgres", "postgres-1", "generation-1", []byte("complete dump"), time.Unix(60, 0),
	)
	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(built.WorkDirectory)
	succeeded := make(chan struct{}, 1)
	service, err := NewResourceRestoreService(ResourceRestoreServiceConfig{
		Context: ctx, Store: store, Target: target, TargetGate: targetGate,
		Admission: admission.New(), Master: master,
		Restorers: map[string]ResourceRestorer{
			"postgres": ResourceRestorerFunc(func(_ context.Context, request ResourceRestoreRequest) error {
				buffer := make([]byte, 1)
				_, err := request.Source.Reader.Read(buffer)
				return err
			}),
		},
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return remote, nil },
		Now:           func() time.Time { return time.Unix(70, 0) },
		OnSuccess:     func(ResourceRestoreRequest) { succeeded <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.Start(
		ctx, "postgres", "postgres-1", "generation-1", ResourceRestoreOptions{},
		Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForOperation(t, store, operation.ID)
	if finished.Status != "failed" || finished.ErrorCode != "postgres_restore_failed" {
		t.Fatalf("failed operation = %+v", finished)
	}
	select {
	case <-succeeded:
		t.Fatal("partial restore called success callback")
	default:
	}
}

func TestResourceRestoreServiceDoesNotCreateOperationForMissingGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, target, targetGate, master := resourceJobTarget(t, root)
	defer store.Close()
	service, err := NewResourceRestoreService(ResourceRestoreServiceConfig{
		Context: ctx, Store: store, Target: target, TargetGate: targetGate,
		Admission: admission.New(), Master: master,
		Restorers: map[string]ResourceRestorer{
			"redis": ResourceRestorerFunc(func(context.Context, ResourceRestoreRequest) error { return nil }),
		},
		RemoteFactory: func(remotes3.Config) (ControlRemote, error) { return newMemoryControlRemote(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(
		ctx, "redis", "redis-1", "missing", ResourceRestoreOptions{},
		Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	); err != ErrResourceGenerationNotFound {
		t.Fatalf("missing generation error = %v", err)
	}
	var count int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM operations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("operation count = %d, %v", count, err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "work")); err == nil && len(entries) != 0 {
		t.Fatalf("unexpected restore work entries = %v", entries)
	}
}

func waitForOperation(t *testing.T, store *state.Store, operationID string) state.Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := store.Operation(context.Background(), operationID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Status != "running" {
			return operation
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return state.Operation{}
}
