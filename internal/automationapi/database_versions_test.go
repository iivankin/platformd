package automationapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/state"
)

type automationVersionStore struct {
	mu         sync.Mutex
	operations map[string]state.Operation
}

func (store *automationVersionStore) BeginOperation(_ context.Context, input state.BeginOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.operations[input.ID] = state.Operation{
		ID: input.ID, Kind: input.Kind, TargetID: input.TargetID,
		Status: "running", Progress: input.Progress, StartedAtMillis: input.StartedAtMillis,
	}
	return nil
}

func (store *automationVersionStore) SetOperationProgress(_ context.Context, id, progress string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[id]
	operation.Progress = progress
	store.operations[id] = operation
	return nil
}

func (store *automationVersionStore) FinishOperation(_ context.Context, input state.FinishOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[input.ID]
	operation.Status = input.Status
	operation.Progress = input.Progress
	operation.FinishedAtMillis = input.FinishedAtMillis
	store.operations[input.ID] = operation
	return nil
}

func (store *automationVersionStore) Operation(_ context.Context, id string) (state.Operation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation, exists := store.operations[id]
	if !exists {
		return state.Operation{}, state.ErrOperationNotFound
	}
	return operation, nil
}

type automationVersionAdapter struct {
	changed chan databaseversion.ChangeRequest
}

func (*automationVersionAdapter) Resource(_ context.Context, projectID, resourceID string) (databaseversion.Resource, error) {
	if projectID != "project" || resourceID != "redis" {
		return databaseversion.Resource{}, state.ErrManagedRedisNotFound
	}
	return databaseversion.Resource{
		ID: "redis", ProjectID: "project", ImageTag: "7.4", ImageDigest: "sha256:source",
	}, nil
}

func (*automationVersionAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:target", nil
}

func (*automationVersionAdapter) Capacity(context.Context, databaseversion.Resource) (databaseversion.Capacity, error) {
	return databaseversion.Capacity{CurrentDataBytes: 10, RequiredFreeBytes: 20, AvailableBytes: 30}, nil
}

func (adapter *automationVersionAdapter) Change(_ context.Context, request databaseversion.ChangeRequest) error {
	adapter.changed <- request
	return nil
}

func TestAutomationStartsDatabaseVersionChangeWithinTokenBoundary(t *testing.T) {
	store := &automationVersionStore{operations: make(map[string]state.Operation)}
	adapter := &automationVersionAdapter{changed: make(chan databaseversion.ChangeRequest, 1)}
	service, err := databaseversion.New(databaseversion.Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]databaseversion.Adapter{databaseversion.Redis: adapter},
		Random:   bytes.NewReader(make([]byte, 64)), Now: func() time.Time { return time.UnixMilli(10) },
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	preview := httptest.NewRequest(http.MethodPost, "/version-change/preview", strings.NewReader(`{"imageTag":"8.0"}`))
	preview.SetPathValue("projectID", projectID)
	preview.SetPathValue("kind", databaseversion.Redis)
	preview.SetPathValue("resourceID", "redis")
	preview.Header.Set("Content-Type", "application/json")
	preview = preview.WithContext(automation.WithIdentity(preview.Context(), automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}))
	previewResponse := httptest.NewRecorder()
	previewDatabaseVersionChange(service).ServeHTTP(previewResponse, preview)
	if previewResponse.Code != http.StatusOK ||
		!strings.Contains(previewResponse.Body.String(), `"requiredFreeBytes":20`) ||
		!strings.Contains(previewResponse.Body.String(), `"ready":true`) || len(store.operations) != 0 {
		t.Fatalf("version preview = %d/%s operations=%d", previewResponse.Code, previewResponse.Body.String(), len(store.operations))
	}
	request := httptest.NewRequest(http.MethodPost, "/version-change", strings.NewReader(`{"imageTag":"8.0","expectedTargetDigest":"sha256:target"}`))
	request.SetPathValue("projectID", projectID)
	request.SetPathValue("kind", databaseversion.Redis)
	request.SetPathValue("resourceID", "redis")
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}))
	response := httptest.NewRecorder()
	startDatabaseVersionChange(service).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") == "" ||
		!strings.Contains(response.Body.String(), `"targetDigest":"sha256:target"`) {
		t.Fatalf("version start = %d/%v/%s", response.Code, response.Header(), response.Body.String())
	}
	change := <-adapter.changed
	if change.Actor != (databaseversion.Actor{Kind: "token", ID: "admin-token"}) || change.ImageTag != "8.0" {
		t.Fatalf("version change = %+v", change)
	}

	otherProject := "other"
	denied := httptest.NewRequest(http.MethodPost, "/version-change", strings.NewReader(`{"imageTag":"8.1","expectedTargetDigest":"sha256:target"}`))
	denied.SetPathValue("projectID", otherProject)
	denied.SetPathValue("kind", databaseversion.Redis)
	denied.SetPathValue("resourceID", "redis")
	denied.Header.Set("Content-Type", "application/json")
	denied = denied.WithContext(automation.WithIdentity(denied.Context(), automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}))
	deniedResponse := httptest.NewRecorder()
	startDatabaseVersionChange(service).ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-project version start = %d/%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestAutomationOpenAPIAdvertisesDatabaseVersionRoutesOnlyWhenConfigured(t *testing.T) {
	without := httptest.NewRecorder()
	serveOpenAPI("api.example.com", openAPIFeatures{}).ServeHTTP(without, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if strings.Contains(without.Body.String(), "version-change") {
		t.Fatalf("unconfigured OpenAPI contains version change: %s", without.Body.String())
	}
	with := httptest.NewRecorder()
	serveOpenAPI("api.example.com", openAPIFeatures{databaseVersions: true}).ServeHTTP(with, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if !strings.Contains(with.Body.String(), "/managed-databases/{kind}/{resourceID}/version-change") ||
		!strings.Contains(with.Body.String(), "/version-change/preview") ||
		!strings.Contains(with.Body.String(), "DatabaseVersionPreviewRequest") ||
		!strings.Contains(with.Body.String(), "DatabaseVersionStartRequest") {
		t.Fatalf("configured OpenAPI lacks version change: %s", with.Body.String())
	}
}
