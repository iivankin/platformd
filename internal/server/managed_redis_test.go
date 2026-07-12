package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

const managedRedisTestDigest = "sha256:3b26d8c8e877651e756205368bbee1163b621f62e7e09577957d6ef4d7e455a4"

type managedRedisRepository struct {
	input    managedredis.CreateInput
	resource state.ManagedRedis
}

func (repository *managedRedisRepository) Create(_ context.Context, input managedredis.CreateInput) (managedredis.CreateResult, error) {
	repository.input = input
	return managedredis.CreateResult{Resource: repository.resource, Password: "shown-once", RequestID: "request-id"}, nil
}

func (repository *managedRedisRepository) Resource(_ context.Context, projectID, resourceID string) (state.ManagedRedis, error) {
	if projectID != repository.resource.ProjectID || resourceID != repository.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return repository.resource, nil
}

func (repository *managedRedisRepository) Resources(_ context.Context, projectID string) ([]state.ManagedRedis, error) {
	if projectID != repository.resource.ProjectID {
		return []state.ManagedRedis{}, nil
	}
	return []state.ManagedRedis{repository.resource}, nil
}

func TestManagedRedisAPIReturnsGeneratedPasswordOnlyFromCreate(t *testing.T) {
	t.Parallel()
	repository := &managedRedisRepository{resource: state.ManagedRedis{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
		ImageTag: "7.4", ImageDigest: managedRedisTestDigest, CPUMillicores: 250,
		MemoryMaxBytes: 128 << 20, BackupRetentionCount: 7, CreatedAtMillis: 10,
		UpdatedAtMillis: 10,
	}}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithManagedRedis(repository)),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/redis", `{
  "name":"cache","imageTag":"7.4","cpuMillicores":250,"memoryBytes":134217728
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Location") != "/api/v1/projects/project/redis/redis" || response.Header().Get("X-Request-ID") != "request-id" {
		t.Fatalf("create status/headers = %d/%v: %s", response.Code, response.Header(), response.Body)
	}
	var created map[string]any
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created["password"] != "shown-once" || created["hostname"] != "cache.shop.internal" || created["port"] != float64(6379) {
		t.Fatalf("create response = %v", created)
	}
	if repository.input.Actor != (managedredis.Actor{Kind: "access", ID: "subject", Email: "admin@example.com"}) || repository.input.ImageTag != "7.4" {
		t.Fatalf("create input = %+v", repository.input)
	}

	get := projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis", "")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), "password") {
		t.Fatalf("get status/body = %d/%s", getResponse.Code, getResponse.Body)
	}
	list := projectRequest(http.MethodGet, "/api/v1/projects/project/redis", "")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), "password") || !strings.Contains(listResponse.Body.String(), `"imageDigest"`) {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body)
	}
}
