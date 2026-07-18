package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type postgresStoreStub struct {
	mu             sync.Mutex
	resource       state.ManagedPostgres
	deployment     state.RuntimeDeployment
	queryAudit     state.RecordManagedPostgresQuery
	extensionAudit state.RecordManagedPostgresExtension
	operations     map[string]state.Operation
	finished       chan struct{}
	finishOnce     sync.Once
}

func (*postgresStoreStub) CreateManagedPostgres(context.Context, state.CreateManagedPostgres) (state.ManagedPostgres, error) {
	return state.ManagedPostgres{}, nil
}

func (store *postgresStoreStub) ManagedPostgresInProject(_ context.Context, projectID, resourceID string) (state.ManagedPostgres, error) {
	if projectID != store.resource.ProjectID || resourceID != store.resource.ID {
		return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
	}
	return store.resource, nil
}

func (store *postgresStoreStub) ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{store.resource}, nil
}

func (store *postgresStoreStub) RecordManagedPostgresQuery(_ context.Context, audit state.RecordManagedPostgresQuery) error {
	store.queryAudit = audit
	return nil
}

func (store *postgresStoreStub) RecordManagedPostgresExtension(_ context.Context, audit state.RecordManagedPostgresExtension) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.extensionAudit = audit
	return nil
}

func (store *postgresStoreStub) BeginOperation(_ context.Context, input state.BeginOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.operations == nil {
		store.operations = make(map[string]state.Operation)
	}
	store.operations[input.ID] = state.Operation{
		ID: input.ID, Kind: input.Kind, TargetID: input.TargetID, Status: "running",
		Progress: input.Progress, StartedAtMillis: input.StartedAtMillis,
	}
	return nil
}

func (store *postgresStoreStub) SetOperationProgress(_ context.Context, id, progress string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[id]
	operation.Progress = progress
	store.operations[id] = operation
	return nil
}

func (store *postgresStoreStub) FinishOperation(_ context.Context, input state.FinishOperation) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	operation := store.operations[input.ID]
	operation.Status = input.Status
	operation.Progress = input.Progress
	operation.ErrorCode = input.ErrorCode
	operation.ErrorMessage = input.ErrorMessage
	operation.FinishedAtMillis = input.FinishedAtMillis
	store.operations[input.ID] = operation
	if store.finished != nil {
		store.finishOnce.Do(func() { close(store.finished) })
	}
	return nil
}

func (store *postgresStoreStub) RuntimeDeployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error) {
	return state.RuntimeDeploymentPage{Deployments: []state.RuntimeDeployment{store.deployment}}, nil
}

func (store *postgresStoreStub) RuntimeDeployment(context.Context, string, string, string) (state.RuntimeDeployment, error) {
	return store.deployment, nil
}

type postgresRuntimeStub struct {
	sql              string
	restarted        string
	removed          string
	extensions       []managedpostgres.Extension
	changedExtension string
	installExtension bool
	changed          chan struct{}
	changeOnce       sync.Once
}

func (*postgresRuntimeStub) ResolveManagedPostgresImage(context.Context, string) (string, error) {
	return "sha256:image", nil
}

func (*postgresRuntimeStub) StartManagedPostgres(context.Context, string) error { return nil }

func (runtime *postgresRuntimeStub) ManagedPostgresExtensions(context.Context, string) ([]managedpostgres.Extension, error) {
	return runtime.extensions, nil
}

func (runtime *postgresRuntimeStub) ChangeManagedPostgresExtension(_ context.Context, _ string, name string, install bool, progress func(string)) error {
	progress("creating_extension")
	runtime.changedExtension = name
	runtime.installExtension = install
	if runtime.changed != nil {
		runtime.changeOnce.Do(func() { close(runtime.changed) })
	}
	return nil
}

func (runtime *postgresRuntimeStub) RestartManagedPostgresDeployment(_ context.Context, _, deploymentID string) error {
	runtime.restarted = deploymentID
	return nil
}

func (runtime *postgresRuntimeStub) RemoveManagedPostgresDeployment(_ context.Context, _, deploymentID string) error {
	runtime.removed = deploymentID
	return nil
}

func (runtime *postgresRuntimeStub) QueryManagedPostgres(_ context.Context, _ string, sql string) (managedpostgres.QueryResult, error) {
	runtime.sql = sql
	return managedpostgres.QueryResult{Statements: []managedpostgres.StatementResult{{
		Columns: []managedpostgres.Column{{Name: "value", TypeOID: 23}},
		Rows:    [][]managedpostgres.Cell{{{Text: "1"}}}, CommandTag: "SELECT 1",
	}}}, nil
}

func TestManagedPostgresQueryIsAccessOnlyAndAuditedWithoutSQL(t *testing.T) {
	store := &postgresStoreStub{resource: state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		DatabaseName: "app", OwnerUsername: "owner",
	}}
	runtime := &postgresRuntimeStub{}
	application, err := managedpostgres.NewApplication(context.Background(), store, runtime, cryptobox.MasterKey{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithManagedPostgres(application))
	protected := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)
	sql := "DELETE FROM sessions; SELECT 1;"
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/postgres/postgres/query", `{"sql":"`+sql+`"}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"commandTag":"SELECT 1"`) || runtime.sql != sql {
		t.Fatalf("query response = %d/%s sql=%q", response.Code, response.Body, runtime.sql)
	}
	if store.queryAudit.ActorEmail != "admin@example.com" || store.queryAudit.Result != "succeeded" || store.queryAudit.RowCount != 1 || store.queryAudit.ErrorClass != "" {
		t.Fatalf("query audit = %+v", store.queryAudit)
	}
	if strings.Contains(response.Body.String(), "DELETE FROM") {
		t.Fatalf("query text leaked in response: %s", response.Body)
	}
	direct := httptest.NewRecorder()
	raw.ServeHTTP(direct, httptest.NewRequest(http.MethodPost, "/api/v1/projects/project/postgres/postgres/query", strings.NewReader(`{"sql":"SELECT 1"}`)))
	if direct.Code != http.StatusForbidden {
		t.Fatalf("query without Access = %d/%s", direct.Code, direct.Body)
	}
}

func TestManagedPostgresExtensionsListAndPrivilegedChange(t *testing.T) {
	store := &postgresStoreStub{finished: make(chan struct{}), resource: state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		DatabaseName: "app", OwnerUsername: "owner",
	}}
	runtime := &postgresRuntimeStub{changed: make(chan struct{}), extensions: []managedpostgres.Extension{{
		Name: "uuid-ossp", DefaultVersion: "1.1", Comment: "UUID generator",
	}}}
	application, err := managedpostgres.NewApplication(context.Background(), store, runtime, cryptobox.MasterKey{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithManagedPostgres(application)),
	)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, projectRequest(http.MethodGet, "/api/v1/projects/project/postgres/postgres/extensions", ""))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"uuid-ossp"`) {
		t.Fatalf("extensions response = %d/%s", list.Code, list.Body)
	}
	change := httptest.NewRecorder()
	request := projectRequest(http.MethodPut, "/api/v1/projects/project/postgres/postgres/extensions/uuid-ossp", "")
	request.Header.Set("Origin", "https://admin.example.com")
	handler.ServeHTTP(change, request)
	if change.Code != http.StatusAccepted || !strings.Contains(change.Body.String(), `"status":"running"`) {
		t.Fatalf("extension change = %d/%s runtime=%+v", change.Code, change.Body, runtime)
	}
	<-runtime.changed
	<-store.finished
	if runtime.changedExtension != "uuid-ossp" || !runtime.installExtension {
		t.Fatalf("extension runtime change = %+v", runtime)
	}
	store.mu.Lock()
	audit := store.extensionAudit
	store.mu.Unlock()
	if audit.ExtensionName != "uuid-ossp" || !audit.Install ||
		audit.ActorEmail != "admin@example.com" || audit.Result != "succeeded" {
		t.Fatalf("extension audit = %+v", audit)
	}
}

func TestManagedPostgresResourceIncludesOwnerPassword(t *testing.T) {
	t.Parallel()
	master := cryptobox.MasterKey{1, 2, 3}
	password := strings.Repeat("a", 43)
	encrypted, err := managedpostgres.SealOwnerPassword(master, "postgres", password)
	if err != nil {
		t.Fatal(err)
	}
	store := &postgresStoreStub{resource: state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		DatabaseName: "app", OwnerUsername: "owner", OwnerPasswordEncrypted: encrypted,
		ImageTag: "17", ImageDigest: "sha256:image", BackupRetentionCount: 7,
		CreatedAtMillis: 10, UpdatedAtMillis: 10,
	}}
	application, err := managedpostgres.NewApplication(context.Background(), store, &postgresRuntimeStub{}, master, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithManagedPostgres(application)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/projects/project/postgres/postgres", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ownerPassword":"`+password+`"`) {
		t.Fatalf("resource response = %d/%s", response.Code, response.Body)
	}
}

func TestManagedPostgresDeploymentHistoryAndActions(t *testing.T) {
	store := &postgresStoreStub{
		resource: state.ManagedPostgres{ID: "postgres", ProjectID: "project"},
		deployment: state.RuntimeDeployment{
			ID: "deployment", ResourceKind: "postgres", ResourceID: "postgres",
			ImageTag: "17", ImageDigest: "sha256:image", Status: "succeeded", Active: true, CreatedAtMillis: 10,
		},
	}
	runtime := &postgresRuntimeStub{}
	application, err := managedpostgres.NewApplication(context.Background(), store, runtime, cryptobox.MasterKey{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithManagedPostgres(application)),
	)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, projectRequest(http.MethodGet, "/api/v1/projects/project/postgres/postgres/deployments", ""))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"id":"deployment"`) || !strings.Contains(list.Body.String(), `"active":true`) {
		t.Fatalf("deployment list = %d/%s", list.Code, list.Body)
	}
	for _, action := range []string{"restart", "remove"} {
		response := httptest.NewRecorder()
		request := projectRequest(http.MethodPost, "/api/v1/projects/project/postgres/postgres/deployments/deployment/"+action, "")
		request.Header.Set("Origin", "https://admin.example.com")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s deployment = %d/%s runtime=%+v", action, response.Code, response.Body, runtime)
		}
	}
	if runtime.restarted != "deployment" || runtime.removed != "deployment" {
		t.Fatalf("deployment actions = %+v", runtime)
	}
}
