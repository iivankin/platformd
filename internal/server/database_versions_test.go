package server_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type serverVersionStore struct {
	mu         sync.Mutex
	operations map[string]state.Operation
}

func (store *serverVersionStore) BeginOperation(_ context.Context, input state.BeginOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.operations[input.ID] = state.Operation{
		ID: input.ID, Kind: input.Kind, TargetID: input.TargetID,
		Status: "running", Progress: input.Progress, StartedAtMillis: input.StartedAtMillis,
	}
	return nil
}

func (store *serverVersionStore) SetOperationProgress(_ context.Context, id, progress string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[id]
	operation.Progress = progress
	store.operations[id] = operation
	return nil
}

func (store *serverVersionStore) FinishOperation(_ context.Context, input state.FinishOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[input.ID]
	operation.Status = input.Status
	operation.Progress = input.Progress
	operation.FinishedAtMillis = input.FinishedAtMillis
	store.operations[input.ID] = operation
	return nil
}

func (store *serverVersionStore) Operation(_ context.Context, id string) (state.Operation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation, exists := store.operations[id]
	if !exists {
		return state.Operation{}, state.ErrOperationNotFound
	}
	return operation, nil
}

type serverVersionAdapter struct {
	request databaseversion.ChangeRequest
	changed chan struct{}
}

func (*serverVersionAdapter) Resource(_ context.Context, projectID, resourceID string) (databaseversion.Resource, error) {
	if projectID != "project" || resourceID != "redis" {
		return databaseversion.Resource{}, state.ErrManagedRedisNotFound
	}
	return databaseversion.Resource{
		ID: "redis", ProjectID: "project", ImageTag: "7.4", ImageDigest: "sha256:source",
	}, nil
}

func (*serverVersionAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:target", nil
}

func (adapter *serverVersionAdapter) Change(_ context.Context, request databaseversion.ChangeRequest) error {
	adapter.request = request
	if adapter.changed != nil {
		close(adapter.changed)
	}
	request.Progress("switching_active_pointer")
	return nil
}

func TestAdminStartsAndReadsRedisVersionChange(t *testing.T) {
	store := &serverVersionStore{operations: make(map[string]state.Operation)}
	adapter := &serverVersionAdapter{changed: make(chan struct{})}
	service, err := databaseversion.New(databaseversion.Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]databaseversion.Adapter{databaseversion.Redis: adapter},
		Random:   bytes.NewReader(make([]byte, 64)), Now: func() time.Time { return time.UnixMilli(10) },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithDatabaseVersions(service)),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/redis/redis/version-change", `{"imageTag":"8.0"}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"targetDigest":"sha256:target"`) ||
		response.Header().Get("Location") == "" {
		t.Fatalf("version start = %d/%v/%s", response.Code, response.Header(), response.Body.String())
	}
	<-adapter.changed
	if adapter.request.Actor != (databaseversion.Actor{Kind: "access", ID: "subject", Email: "admin@example.com"}) ||
		adapter.request.ImageTag != "8.0" {
		t.Fatalf("version actor/request = %+v", adapter.request)
	}
	location := response.Header().Get("Location")
	deadline := time.Now().Add(time.Second)
	for {
		read := projectRequest(http.MethodGet, location, "")
		readResponse := httptest.NewRecorder()
		handler.ServeHTTP(readResponse, read)
		if readResponse.Code == http.StatusOK && strings.Contains(readResponse.Body.String(), `"status":"succeeded"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("version operation did not finish: %d/%s", readResponse.Code, readResponse.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAdminVersionChangeDoesNotRevealCrossProjectOperation(t *testing.T) {
	store := &serverVersionStore{operations: map[string]state.Operation{
		"operation": {ID: "operation", Kind: "redis_version_change", TargetID: "redis", Status: "succeeded"},
	}}
	service, err := databaseversion.New(databaseversion.Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]databaseversion.Adapter{databaseversion.Redis: &serverVersionAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithDatabaseVersions(service)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(
		http.MethodGet, "/api/v1/projects/other/redis/redis/version-change/operation", "",
	))
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-project operation = %d/%s", response.Code, response.Body.String())
	}
}
