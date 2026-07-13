package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type mcpManagedResourceRepository struct {
	redisCalls  int
	backupCalls int
}

func (*mcpManagedResourceRepository) ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		ImageTag: "18", DatabaseName: "app", OwnerUsername: "owner",
		OwnerPasswordEncrypted: []byte("postgres-secret"), BackupRetentionCount: 7,
	}}, nil
}

func (*mcpManagedResourceRepository) ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error) {
	return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
}

func (repository *mcpManagedResourceRepository) ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error) {
	repository.redisCalls++
	return []state.ManagedRedis{{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("redis-secret"), BackupRetentionCount: 3,
	}}, nil
}

func (repository *mcpManagedResourceRepository) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	repository.redisCalls++
	return state.ManagedRedis{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("redis-secret"), BackupRetentionCount: 3,
	}, nil
}

func (*mcpManagedResourceRepository) ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error) {
	return []state.ObjectStore{{
		ID: "store", ProjectID: "project", ProjectName: "shop", Name: "assets",
		BucketName: "assets-bucket", BackupRetentionCount: 5,
	}}, nil
}

func (*mcpManagedResourceRepository) ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error) {
	return state.ObjectStore{}, state.ErrObjectStoreNotFound
}

func (repository *mcpManagedResourceRepository) BackupHistory(context.Context, state.BackupHistoryQuery) ([]state.BackupRecord, error) {
	repository.backupCalls++
	return []state.BackupRecord{{ID: "backup", Status: "succeeded", StartedAtMillis: 10}}, nil
}

func TestMCPManagedResourceMetadataAndBackupsAreProjectScopedAndSecretFree(t *testing.T) {
	repository := &mcpManagedResourceRepository{}
	application, err := automation.NewManagedResourceApplication(repository)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, &repositoryStub{})
	handler.managed = application
	handler.tools = configuredReadTools(true, false)

	response := callMCPTool(t, handler, automation.Identity{TokenID: "read", Role: "read"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_managed_resources","arguments":{"projectId":"project"}}}`)
	if strings.Contains(response, `"isError":true`) || !strings.Contains(response, `assets-bucket`) ||
		!strings.Contains(response, `database`) || !strings.Contains(response, `cache`) ||
		strings.Contains(response, `postgres-secret`) || strings.Contains(response, `redis-secret`) {
		t.Fatalf("managed resource list = %s", response)
	}

	bound := "project"
	response = callMCPTool(t, handler, automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound},
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_managed_resource_backups","arguments":{"projectId":"other","kind":"redis","resourceId":"redis"}}}`)
	if !strings.Contains(response, `"isError":true`) || repository.redisCalls != 1 || repository.backupCalls != 0 {
		t.Fatalf("cross-project backup read = %s calls=%d/%d", response, repository.redisCalls, repository.backupCalls)
	}

	response = callMCPTool(t, handler, automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound},
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_managed_resource_backups","arguments":{"projectId":"project","kind":"redis","resourceId":"redis"}}}`)
	if strings.Contains(response, `"isError":true`) || !strings.Contains(response, `backup`) || repository.backupCalls != 1 {
		t.Fatalf("backup status = %s calls=%d", response, repository.backupCalls)
	}
}

func callMCPTool(t *testing.T, handler *Handler, identity automation.Identity, body string) string {
	t.Helper()
	request := withMCPIdentity(mcpRequest(body), identity)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Body.String()
}
