package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type postgresStoreStub struct {
	resource state.ManagedPostgres
	audit    state.RecordManagedPostgresQuery
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
	store.audit = audit
	return nil
}

type postgresRuntimeStub struct{ sql string }

func (*postgresRuntimeStub) ResolveManagedPostgresImage(context.Context, string) (string, error) {
	return "sha256:image", nil
}

func (*postgresRuntimeStub) StartManagedPostgres(context.Context, string) error { return nil }

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
	application, err := managedpostgres.NewApplication(store, runtime, cryptobox.MasterKey{}, nil, nil)
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
	if store.audit.ActorEmail != "admin@example.com" || store.audit.Result != "succeeded" || store.audit.RowCount != 1 || store.audit.ErrorClass != "" {
		t.Fatalf("query audit = %+v", store.audit)
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
