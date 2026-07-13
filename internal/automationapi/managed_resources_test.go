package automationapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type apiManagedResourceRepository struct {
	redisCalls  int
	backupCalls int
}

func (*apiManagedResourceRepository) ManagedPostgresByProject(context.Context, string) ([]state.ManagedPostgres, error) {
	return []state.ManagedPostgres{{
		ID: "postgres", ProjectID: "project", ProjectName: "shop", Name: "database",
		ImageTag: "18", OwnerPasswordEncrypted: []byte("postgres-secret"), BackupRetentionCount: 7,
	}}, nil
}

func (*apiManagedResourceRepository) ManagedPostgresInProject(context.Context, string, string) (state.ManagedPostgres, error) {
	return state.ManagedPostgres{}, state.ErrManagedPostgresNotFound
}

func (repository *apiManagedResourceRepository) ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error) {
	repository.redisCalls++
	return []state.ManagedRedis{{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("redis-secret"), BackupRetentionCount: 3,
	}}, nil
}

func (repository *apiManagedResourceRepository) ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error) {
	repository.redisCalls++
	return state.ManagedRedis{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "8", PasswordEncrypted: []byte("redis-secret"), BackupRetentionCount: 3,
	}, nil
}

func (*apiManagedResourceRepository) ObjectStoresByProject(context.Context, string) ([]state.ObjectStore, error) {
	return []state.ObjectStore{{
		ID: "store", ProjectID: "project", ProjectName: "shop", Name: "assets",
		BucketName: "assets-bucket", BackupRetentionCount: 5,
	}}, nil
}

func (*apiManagedResourceRepository) ObjectStoreInProject(context.Context, string, string) (state.ObjectStore, error) {
	return state.ObjectStore{}, state.ErrObjectStoreNotFound
}

func (repository *apiManagedResourceRepository) BackupHistory(context.Context, state.BackupHistoryQuery) ([]state.BackupRecord, error) {
	repository.backupCalls++
	return []state.BackupRecord{{ID: "backup", Status: "succeeded", StartedAtMillis: 10}}, nil
}

func TestAutomationManagedResourceReadsAreProjectScopedAndSecretFree(t *testing.T) {
	repository := &apiManagedResourceRepository{}
	application, err := automation.NewManagedResourceApplication(repository)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects/project/managed-resources", nil)
	request.SetPathValue("projectID", "project")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	response := httptest.NewRecorder()
	listManagedResources(application).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"assets-bucket"`) ||
		!strings.Contains(response.Body.String(), `"database"`) || !strings.Contains(response.Body.String(), `"cache"`) ||
		strings.Contains(response.Body.String(), "postgres-secret") || strings.Contains(response.Body.String(), "redis-secret") {
		t.Fatalf("managed resources = %d/%s", response.Code, response.Body.String())
	}

	bound := "project"
	request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects/other/managed-resources/redis/redis", nil)
	request.SetPathValue("projectID", "other")
	request.SetPathValue("kind", "redis")
	request.SetPathValue("resourceID", "redis")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound}))
	response = httptest.NewRecorder()
	getManagedResource(application).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || repository.redisCalls != 1 {
		t.Fatalf("cross-project read = %d/%s calls=%d", response.Code, response.Body.String(), repository.redisCalls)
	}

	request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects/project/managed-resources/redis/redis/backups?limit=10", nil)
	request.SetPathValue("projectID", "project")
	request.SetPathValue("kind", "redis")
	request.SetPathValue("resourceID", "redis")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "bound", Role: "read", ProjectID: &bound}))
	response = httptest.NewRecorder()
	readManagedResourceBackups(application).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"backup"`) || repository.backupCalls != 1 {
		t.Fatalf("backup read = %d/%s calls=%d", response.Code, response.Body.String(), repository.backupCalls)
	}
}

func TestManagedResourceOpenAPIPathsMatchFeatureConfiguration(t *testing.T) {
	response := httptest.NewRecorder()
	serveOpenAPI("api.example.com", false, false).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if strings.Contains(response.Body.String(), `managed-resources`) {
		t.Fatalf("disabled managed resource routes were advertised: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	serveOpenAPI("api.example.com", false, true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if !strings.Contains(response.Body.String(), `/managed-resources`) || !strings.Contains(response.Body.String(), `/backups`) {
		t.Fatalf("managed resource routes are absent: %s", response.Body.String())
	}
}
