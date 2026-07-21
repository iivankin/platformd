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
	input          managedredis.CreateInput
	resource       state.ManagedRedis
	mutation       managedredis.DataMutationInput
	deploymentPage state.RuntimeDeploymentPage
}

func (repository *managedRedisRepository) Create(_ context.Context, input managedredis.CreateInput) (managedredis.CreateResult, error) {
	repository.input = input
	return managedredis.CreateResult{Resource: repository.resource, Password: "created-password", RequestID: "request-id"}, nil
}

func (repository *managedRedisRepository) Resource(_ context.Context, projectID, resourceID string) (state.ManagedRedis, error) {
	if projectID != repository.resource.ProjectID || resourceID != repository.resource.ID {
		return state.ManagedRedis{}, state.ErrManagedRedisNotFound
	}
	return repository.resource, nil
}

func (repository *managedRedisRepository) Password(_ context.Context, projectID, resourceID string) (string, error) {
	if projectID != repository.resource.ProjectID || resourceID != repository.resource.ID {
		return "", state.ErrManagedRedisNotFound
	}
	return "always-visible", nil
}

func (repository *managedRedisRepository) Resources(_ context.Context, projectID string) ([]state.ManagedRedis, error) {
	if projectID != repository.resource.ProjectID {
		return []state.ManagedRedis{}, nil
	}
	return []state.ManagedRedis{repository.resource}, nil
}

func (*managedRedisRepository) Persistence(context.Context, string, string) (managedredis.PersistenceReport, error) {
	return managedredis.PersistenceReport{
		ObservedAtMillis: 1_700_000_600_000, LastSuccessfulSaveAtMillis: 1_700_000_000_000,
		ActualRPOMillis: 600_000, TargetRPOMillis: 300_000,
		LastBackgroundSaveSuccessful: true, NeedsAttention: true,
	}, nil
}

func (*managedRedisRepository) Stats(context.Context, string, string) (managedredis.Stats, error) {
	return managedredis.Stats{}, nil
}

func (*managedRedisRepository) Keys(context.Context, string, string, managedredis.ScanQuery) (managedredis.KeyPage, error) {
	ttl := int64(1500)
	return managedredis.KeyPage{NextCursor: 7, Keys: []managedredis.KeySummary{{
		Key: []byte("user:1"), Type: "string", ExpiresInMillis: &ttl, SizeBytes: 96,
	}, {Key: []byte{0xff, 0x00}, Type: "hash", SizeBytes: 128}}}, nil
}

func (*managedRedisRepository) Preview(context.Context, string, string, managedredis.PreviewQuery) (managedredis.Preview, error) {
	return managedredis.Preview{Type: "string", Length: 5, Items: []managedredis.PreviewItem{{Values: [][]byte{[]byte("hello")}}}}, nil
}

func (repository *managedRedisRepository) Mutate(_ context.Context, input managedredis.DataMutationInput) (managedredis.DataMutationResult, error) {
	repository.mutation = input
	return managedredis.DataMutationResult{MutationResult: managedredis.MutationResult{Affected: 1}, RequestID: "mutation-request", AuditRecorded: true}, nil
}

func (repository *managedRedisRepository) Deployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error) {
	return repository.deploymentPage, nil
}

func (*managedRedisRepository) Deployment(context.Context, string, string, string) (state.RuntimeDeployment, error) {
	return state.RuntimeDeployment{}, nil
}

func (*managedRedisRepository) RestartDeployment(context.Context, string, string, string) error {
	return nil
}

func (*managedRedisRepository) RemoveDeployment(context.Context, string, string, string) error {
	return nil
}

func TestManagedRedisAPIReturnsPasswordFromCreateAndResourceDetails(t *testing.T) {
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
  "name":"cache","imageTag":"7.4","cpuMillicores":250,"memoryBytes":134217728,
  "credentials":{"password":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
  "backupPolicy":{"targetId":"backup-target","enabled":true,"cron":"0 3 * * *","retentionCount":12}
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
	if created["password"] != "created-password" || created["hostname"] != "cache.shop.internal" || created["port"] != float64(6379) {
		t.Fatalf("create response = %v", created)
	}
	if repository.input.Actor != (managedredis.Actor{Kind: "access", ID: "subject", Email: "admin@example.com"}) || repository.input.ImageTag != "7.4" ||
		repository.input.BackupPolicy != (state.InitialBackupPolicy{TargetID: "backup-target", Enabled: true, Cron: "0 3 * * *", RetentionCount: 12}) ||
		repository.input.Credentials == nil || repository.input.Credentials.Password != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("create input = %+v", repository.input)
	}

	get := projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis", "")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"password":"always-visible"`) {
		t.Fatalf("get status/body = %d/%s", getResponse.Code, getResponse.Body)
	}
	list := projectRequest(http.MethodGet, "/api/v1/projects/project/redis", "")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), "password") || !strings.Contains(listResponse.Body.String(), `"imageDigest"`) {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body)
	}
	persistence := projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis/persistence", "")
	persistenceResponse := httptest.NewRecorder()
	handler.ServeHTTP(persistenceResponse, persistence)
	if persistenceResponse.Code != http.StatusOK ||
		!strings.Contains(persistenceResponse.Body.String(), `"actualRpoMillis":600000`) ||
		!strings.Contains(persistenceResponse.Body.String(), `"lastSuccessfulSaveAt":1700000000000`) ||
		!strings.Contains(persistenceResponse.Body.String(), `"needsAttention":true`) {
		t.Fatalf("persistence status/body = %d/%s", persistenceResponse.Code, persistenceResponse.Body)
	}
	keys := projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis/keys?count=2", "")
	keysResponse := httptest.NewRecorder()
	handler.ServeHTTP(keysResponse, keys)
	if keysResponse.Code != http.StatusOK || !strings.Contains(keysResponse.Body.String(), `"nextCursor":"7"`) || !strings.Contains(keysResponse.Body.String(), `"keyText":"user:1"`) || !strings.Contains(keysResponse.Body.String(), `"keyBase64":"_wA"`) {
		t.Fatalf("keys status/body = %d/%s", keysResponse.Code, keysResponse.Body)
	}
	preview := projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis/preview?key=dXNlcjox", "")
	previewResponse := httptest.NewRecorder()
	handler.ServeHTTP(previewResponse, preview)
	if previewResponse.Code != http.StatusOK || !strings.Contains(previewResponse.Body.String(), `"type":"string"`) || !strings.Contains(previewResponse.Body.String(), `"text":"hello"`) {
		t.Fatalf("preview status/body = %d/%s", previewResponse.Code, previewResponse.Body)
	}
	mutation := projectRequest(http.MethodPost, "/api/v1/projects/project/redis/redis/data/mutations", `{
  "operation":"hash_set","key":"a2V5","field":"ZmllbGQ","value":"dmFsdWU"
}`)
	mutation.Header.Set("Origin", "https://admin.example.com")
	mutationResponse := httptest.NewRecorder()
	handler.ServeHTTP(mutationResponse, mutation)
	if mutationResponse.Code != http.StatusOK || mutationResponse.Header().Get("X-Request-ID") != "mutation-request" || !strings.Contains(mutationResponse.Body.String(), `"auditRecorded":true`) {
		t.Fatalf("mutation status/body = %d/%s", mutationResponse.Code, mutationResponse.Body)
	}
	if repository.mutation.Actor.Kind != "access" || repository.mutation.Actor.ID != "subject" || string(repository.mutation.Mutation.Key) != "key" || string(repository.mutation.Mutation.Field) != "field" || string(repository.mutation.Mutation.Value) != "value" {
		t.Fatalf("mutation input = %+v", repository.mutation)
	}
}

func TestManagedRedisTerminalDeploymentPageOmitsEmptyCursor(t *testing.T) {
	t.Parallel()
	repository := &managedRedisRepository{resource: state.ManagedRedis{
		ID: "redis", ProjectID: "project", ProjectName: "shop", Name: "cache",
	}, deploymentPage: state.RuntimeDeploymentPage{Deployments: []state.RuntimeDeployment{{
		ID: "deployment", ResourceKind: "redis", ResourceID: "redis",
		ImageTag: "7.4", ImageDigest: managedRedisTestDigest, Status: "succeeded",
		Active: true, CreatedAtMillis: 1_700_000_000_000,
	}}}}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithManagedRedis(repository)),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/projects/project/redis/redis/deployments", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("deployment history = %d/%s", response.Code, response.Body)
	}
	var page map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page["deployments"]), `"id":"deployment"`) {
		t.Fatalf("deployments = %s", page["deployments"])
	}
	if _, exists := page["nextCursor"]; exists {
		t.Fatalf("empty cursor was serialized: %s", page["nextCursor"])
	}
}
