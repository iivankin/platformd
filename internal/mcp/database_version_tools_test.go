package mcp

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/state"
)

type mcpVersionStore struct {
	mu         sync.Mutex
	operations map[string]state.Operation
}

func (store *mcpVersionStore) BeginOperation(_ context.Context, input state.BeginOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.operations[input.ID] = state.Operation{
		ID: input.ID, Kind: input.Kind, TargetID: input.TargetID,
		Status: "running", Progress: input.Progress, StartedAtMillis: input.StartedAtMillis,
	}
	return nil
}

func (store *mcpVersionStore) SetOperationProgress(_ context.Context, id, progress string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[id]
	operation.Progress = progress
	store.operations[id] = operation
	return nil
}

func (store *mcpVersionStore) FinishOperation(_ context.Context, input state.FinishOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[input.ID]
	operation.Status = input.Status
	operation.Progress = input.Progress
	operation.FinishedAtMillis = input.FinishedAtMillis
	store.operations[input.ID] = operation
	return nil
}

func (store *mcpVersionStore) Operation(_ context.Context, id string) (state.Operation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation, exists := store.operations[id]
	if !exists {
		return state.Operation{}, state.ErrOperationNotFound
	}
	return operation, nil
}

type mcpVersionAdapter struct {
	changed chan databaseversion.ChangeRequest
}

func (*mcpVersionAdapter) Resource(_ context.Context, projectID, resourceID string) (databaseversion.Resource, error) {
	if projectID != "project" || resourceID != "redis" {
		return databaseversion.Resource{}, state.ErrManagedRedisNotFound
	}
	return databaseversion.Resource{
		ID: "redis", ProjectID: "project", ImageTag: "7.4", ImageDigest: "sha256:source",
	}, nil
}

func (*mcpVersionAdapter) Resolve(context.Context, string) (string, error) {
	return "sha256:target", nil
}

func (*mcpVersionAdapter) Capacity(context.Context, databaseversion.Resource) (databaseversion.Capacity, error) {
	return databaseversion.Capacity{CurrentDataBytes: 10, RequiredFreeBytes: 20, AvailableBytes: 30}, nil
}

func (adapter *mcpVersionAdapter) Change(_ context.Context, request databaseversion.ChangeRequest) error {
	adapter.changed <- request
	return nil
}

func TestMCPStartsAndReadsDatabaseVersionChangeWithinTokenBoundary(t *testing.T) {
	store := &mcpVersionStore{operations: make(map[string]state.Operation)}
	adapter := &mcpVersionAdapter{changed: make(chan databaseversion.ChangeRequest, 1)}
	service, err := databaseversion.New(databaseversion.Config{
		Context: context.Background(), Store: store, Admission: admission.New(),
		Adapters: map[string]databaseversion.Adapter{databaseversion.Redis: adapter},
		Random:   bytes.NewReader(make([]byte, 64)), Now: func() time.Time { return time.UnixMilli(10) },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{
		hostname: "api.example.com", version: "test", versions: service,
		tools: configuredReadTools(false, true), admission: admission.New(),
	}
	projectID := "project"
	preview := callMCPTool(t, handler, automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}, `{"jsonrpc":"2.0","id":0,"method":"tools/call","params":{"name":"preview_managed_database_version_change","arguments":{"projectId":"project","kind":"redis","resourceId":"redis","imageTag":"8.0"}}}`)
	if !strings.Contains(preview, `\"targetDigest\":\"sha256:target\"`) ||
		!strings.Contains(preview, `\"requiredFreeBytes\":20`) || !strings.Contains(preview, `\"ready\":true`) ||
		len(store.operations) != 0 {
		t.Fatalf("MCP version preview = %s operations=%d", preview, len(store.operations))
	}
	output := callMCPTool(t, handler, automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"start_managed_database_version_change","arguments":{"projectId":"project","kind":"redis","resourceId":"redis","imageTag":"8.0"}}}`)
	if !strings.Contains(output, `\"targetDigest\":\"sha256:target\"`) || !strings.Contains(output, `\"status\":\"running\"`) {
		t.Fatalf("MCP version start = %s", output)
	}
	change := <-adapter.changed
	if change.Actor != (databaseversion.Actor{Kind: "token", ID: "admin-token"}) {
		t.Fatalf("version actor = %+v", change.Actor)
	}

	denied := callMCPTool(t, handler, automation.Identity{
		TokenID: "admin-token", Role: "admin", ProjectID: &projectID,
	}, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"start_managed_database_version_change","arguments":{"projectId":"other","kind":"redis","resourceId":"redis","imageTag":"8.1"}}}`)
	if !strings.Contains(denied, automation.ErrProjectBoundary.Error()) {
		t.Fatalf("cross-project MCP version start = %s", denied)
	}
}

func TestMCPVersionMutationToolIsAdminOnly(t *testing.T) {
	tools := configuredReadTools(false, true)
	if !containsTool(tools, "read_managed_database_version_change") ||
		containsTool(tools, "preview_managed_database_version_change") ||
		containsTool(tools, "start_managed_database_version_change") {
		t.Fatalf("read tool set = %+v", tools)
	}
	admin := adminTools()
	admin = append(admin, previewDatabaseVersionTool(), startDatabaseVersionTool())
	if !containsTool(admin, "preview_managed_database_version_change") ||
		!containsTool(admin, "start_managed_database_version_change") ||
		!isAdminMutationTool("preview_managed_database_version_change") ||
		!isAdminMutationTool("start_managed_database_version_change") {
		t.Fatalf("admin tool set = %+v", admin)
	}
}

func containsTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
