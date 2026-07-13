package databaseversion

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/state"
)

type versionStore struct {
	mu         sync.Mutex
	operations map[string]state.Operation
}

func (store *versionStore) BeginOperation(_ context.Context, input state.BeginOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.operations[input.ID] = state.Operation{
		ID: input.ID, Kind: input.Kind, TargetID: input.TargetID,
		Status: "running", Progress: input.Progress, StartedAtMillis: input.StartedAtMillis,
	}
	return nil
}

func (store *versionStore) SetOperationProgress(_ context.Context, id, progress string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[id]
	operation.Progress = progress
	store.operations[id] = operation
	return nil
}

func (store *versionStore) FinishOperation(_ context.Context, input state.FinishOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[input.ID]
	operation.Status = input.Status
	operation.Progress = input.Progress
	operation.ErrorCode = input.ErrorCode
	operation.ErrorMessage = input.ErrorMessage
	operation.FinishedAtMillis = input.FinishedAtMillis
	store.operations[input.ID] = operation
	return nil
}

func (store *versionStore) Operation(_ context.Context, id string) (state.Operation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation, exists := store.operations[id]
	if !exists {
		return state.Operation{}, state.ErrOperationNotFound
	}
	return operation, nil
}

type versionAdapter struct {
	started chan struct{}
	release chan struct{}
	request ChangeRequest
}

func (*versionAdapter) Resource(_ context.Context, projectID, resourceID string) (Resource, error) {
	if projectID != "project" || resourceID != "redis" {
		return Resource{}, state.ErrManagedRedisNotFound
	}
	return Resource{
		ID: "redis", ProjectID: "project", ImageTag: "7.4", ImageDigest: "sha256:source",
	}, nil
}

func (*versionAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:target", nil
}

func (*versionAdapter) Capacity(context.Context, Resource) (Capacity, error) {
	return Capacity{CurrentDataBytes: 40, RequiredFreeBytes: 50, AvailableBytes: 100}, nil
}

func (adapter *versionAdapter) Change(_ context.Context, request ChangeRequest) error {
	adapter.request = request
	close(adapter.started)
	request.Progress("copying_data")
	<-adapter.release
	return nil
}

func TestServiceRunsOneInMemoryVersionChangeAndKeepsOperationObservational(t *testing.T) {
	store := &versionStore{operations: make(map[string]state.Operation)}
	adapter := &versionAdapter{started: make(chan struct{}), release: make(chan struct{})}
	service, err := New(Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]Adapter{Redis: adapter}, Random: bytes.NewReader(make([]byte, 64)),
		Now: func() time.Time { return time.UnixMilli(10) },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(
		context.Background(), Redis, "project", "redis", "8.0", "sha256:target",
		Actor{Kind: "token", ID: "admin"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceTag != "7.4" || result.TargetTag != "8.0" || result.TargetDigest != "sha256:target" ||
		result.Operation.Status != "running" {
		t.Fatalf("start result = %+v", result)
	}
	<-adapter.started
	if _, err := service.Start(
		context.Background(), Redis, "project", "redis", "8.1", "sha256:target", Actor{Kind: "token", ID: "admin"},
	); !errors.Is(err, ErrResourceBusy) {
		t.Fatalf("concurrent start error = %v", err)
	}
	if adapter.request.Resource.ID != "redis" || adapter.request.ImageDigest != "sha256:target" ||
		adapter.request.Actor.ID != "admin" {
		t.Fatalf("adapter request = %+v", adapter.request)
	}
	close(adapter.release)
	deadline := time.Now().Add(time.Second)
	for {
		operation, err := service.Operation(context.Background(), Redis, "project", "redis", result.Operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Status == "succeeded" {
			if operation.Progress != "complete" {
				t.Fatalf("finished operation = %+v", operation)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not finish: %+v", operation)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := service.Operation(context.Background(), Redis, "other", "redis", result.Operation.ID); !errors.Is(err, state.ErrManagedRedisNotFound) {
		t.Fatalf("cross-project operation read = %v", err)
	}
}

func TestServiceRejectsSameDigestBeforeCreatingOperation(t *testing.T) {
	store := &versionStore{operations: make(map[string]state.Operation)}
	service, err := New(Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]Adapter{Redis: sameDigestAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Start(
		context.Background(), Redis, "project", "redis", "7.4", "sha256:same", Actor{Kind: "token", ID: "admin"},
	)
	if !errors.Is(err, ErrSameDigest) || len(store.operations) != 0 {
		t.Fatalf("same digest result = %v, operations=%d", err, len(store.operations))
	}
}

func TestServiceRejectsTargetDigestThatMovedAfterPreview(t *testing.T) {
	store := &versionStore{operations: make(map[string]state.Operation)}
	service, err := New(Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]Adapter{Redis: &versionAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Start(
		context.Background(), Redis, "project", "redis", "8.0", "sha256:previewed",
		Actor{Kind: "token", ID: "admin"},
	)
	if !errors.Is(err, ErrTargetDigestMoved) || len(store.operations) != 0 {
		t.Fatalf("moved target result = %v, operations=%d", err, len(store.operations))
	}
}

func TestServicePreviewReportsCapacityWithoutCreatingOperation(t *testing.T) {
	store := &versionStore{operations: make(map[string]state.Operation)}
	service, err := New(Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]Adapter{Redis: &versionAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(context.Background(), Redis, "project", "redis", " 8.0 ")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Ready || preview.Blocker != "" || preview.TargetTag != "8.0" ||
		preview.CurrentDataBytes != 40 || preview.RequiredFreeBytes != 50 || preview.AvailableFreeBytes != 100 {
		t.Fatalf("preview = %+v", preview)
	}
	if len(store.operations) != 0 {
		t.Fatalf("preview created %d operations", len(store.operations))
	}
}

func TestServiceRejectsInsufficientSpaceBeforeCreatingOperation(t *testing.T) {
	store := &versionStore{operations: make(map[string]state.Operation)}
	service, err := New(Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]Adapter{Redis: insufficientCapacityAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(context.Background(), Redis, "project", "redis", "8.0")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ready || preview.Blocker != BlockerInsufficientSpace {
		t.Fatalf("preview = %+v", preview)
	}
	_, err = service.Start(
		context.Background(), Redis, "project", "redis", "8.0", "sha256:target", Actor{Kind: "token", ID: "admin"},
	)
	if !errors.Is(err, ErrInsufficientSpace) || len(store.operations) != 0 {
		t.Fatalf("insufficient capacity result = %v, operations=%d", err, len(store.operations))
	}
}

type sameDigestAdapter struct{}

func (sameDigestAdapter) Resource(context.Context, string, string) (Resource, error) {
	return Resource{ID: "redis", ProjectID: "project", ImageDigest: "sha256:same"}, nil
}

func (sameDigestAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:same", nil
}

func (sameDigestAdapter) Capacity(context.Context, Resource) (Capacity, error) {
	return Capacity{AvailableBytes: 1}, nil
}

func (sameDigestAdapter) Change(context.Context, ChangeRequest) error {
	return errors.New("must not run")
}

type insufficientCapacityAdapter struct{}

func (insufficientCapacityAdapter) Resource(context.Context, string, string) (Resource, error) {
	return Resource{ID: "redis", ProjectID: "project", ImageDigest: "sha256:source"}, nil
}

func (insufficientCapacityAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:target", nil
}

func (insufficientCapacityAdapter) Capacity(context.Context, Resource) (Capacity, error) {
	return Capacity{CurrentDataBytes: 90, RequiredFreeBytes: 100, AvailableBytes: 99}, nil
}

func (insufficientCapacityAdapter) Change(context.Context, ChangeRequest) error {
	return errors.New("must not run")
}
