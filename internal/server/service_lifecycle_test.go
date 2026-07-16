package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type serviceDeploymentActionRepository struct {
	*state.Store
	restarted string
	removed   string
}

func (repository *serviceDeploymentActionRepository) RestartServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	repository.restarted = input.DeploymentID
	return repository.DesiredService(ctx, input.ID)
}

func (repository *serviceDeploymentActionRepository) RemoveServiceDeployment(ctx context.Context, input state.DeleteServiceDeploymentInput) (state.ServiceDesired, error) {
	repository.removed = input.DeploymentID
	return repository.DesiredService(ctx, input.ID)
}

func TestServiceLifecycleAPIUpdatesListsDeploymentsAndDeploysVersion(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{ImageReference: "alpine:3.22"},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, snapshotJSON, hash, err := serviceconfig.Canonical(service.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:5f70bf18a08660b3c3e431d73e3a1b13f1f4f9f365f22c4b155b87f12ee41a68"
	if err := store.BeginDeployment(context.Background(), state.BeginDeployment{
		ID: "deployment", ServiceID: service.ID, ImageDigest: digest,
		ConfigHash: hash, SnapshotJSON: snapshotJSON, CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateDeployment(context.Background(), service.ID, "deployment", "", 4); err != nil {
		t.Fatal(err)
	}

	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServices(store)),
	)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service", ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"activeDeploymentId":"deployment"`) {
		t.Fatalf("get status/body = %d/%s", getResponse.Code, getResponse.Body)
	}
	var current map[string]any
	if err := json.NewDecoder(getResponse.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}
	expectedUpdated := int64(current["updatedAt"].(float64))

	update := projectRequest(http.MethodPut, "/api/v1/projects/project/services/service", `{
  "imageReference":"alpine:3.23",
  "enabled":false,
	"expectedUpdatedAt":`+strconv.FormatInt(expectedUpdated, 10)+`
}`)
	update.Header.Set("Origin", "https://admin.example.com")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK || !strings.Contains(updateResponse.Body.String(), `"enabled":false`) || strings.Contains(updateResponse.Body.String(), `"activeDeploymentId"`) {
		t.Fatalf("update status/body = %d/%s", updateResponse.Code, updateResponse.Body)
	}
	var updated map[string]any
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	updatedAt := int64(updated["updatedAt"].(float64))

	stale := projectRequest(http.MethodPut, "/api/v1/projects/project/services/service", `{
  "imageReference":"alpine:3.24",
  "enabled":true,
	"expectedUpdatedAt":`+strconv.FormatInt(expectedUpdated, 10)+`
}`)
	stale.Header.Set("Origin", "https://admin.example.com")
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), `"code":"service_changed"`) {
		t.Fatalf("stale status/body = %d/%s", staleResponse.Code, staleResponse.Body)
	}

	deploymentsResponse := httptest.NewRecorder()
	handler.ServeHTTP(deploymentsResponse, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/deployments?limit=1", ""))
	if deploymentsResponse.Code != http.StatusOK || !strings.Contains(deploymentsResponse.Body.String(), `"id":"deployment"`) {
		t.Fatalf("deployments status/body = %d/%s", deploymentsResponse.Code, deploymentsResponse.Body)
	}

	deploymentResponse := httptest.NewRecorder()
	handler.ServeHTTP(deploymentResponse, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/deployments/deployment", ""))
	if deploymentResponse.Code != http.StatusOK || !strings.Contains(deploymentResponse.Body.String(), `"id":"deployment"`) {
		t.Fatalf("deployment status/body = %d/%s", deploymentResponse.Code, deploymentResponse.Body)
	}
	missingDeploymentResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingDeploymentResponse, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/deployments/missing", ""))
	if missingDeploymentResponse.Code != http.StatusNotFound || !strings.Contains(missingDeploymentResponse.Body.String(), `"code":"deployment_not_found"`) {
		t.Fatalf("missing deployment status/body = %d/%s", missingDeploymentResponse.Code, missingDeploymentResponse.Body)
	}

	deployVersion := projectRequest(http.MethodPost, "/api/v1/projects/project/services/service/deployments/deployment/deploy", `{
	"expectedUpdatedAt":`+strconv.FormatInt(updatedAt, 10)+`
}`)
	deployVersion.Header.Set("Origin", "https://admin.example.com")
	deployVersionResponse := httptest.NewRecorder()
	handler.ServeHTTP(deployVersionResponse, deployVersion)
	if deployVersionResponse.Code != http.StatusOK || !strings.Contains(deployVersionResponse.Body.String(), "@"+digest) || !strings.Contains(deployVersionResponse.Body.String(), `"enabled":false`) {
		t.Fatalf("deploy version status/body = %d/%s", deployVersionResponse.Code, deployVersionResponse.Body)
	}
	var deployed map[string]any
	if err := json.NewDecoder(deployVersionResponse.Body).Decode(&deployed); err != nil {
		t.Fatal(err)
	}
	deleteRequest := projectRequest(http.MethodDelete, "/api/v1/projects/project/services/service", `{"expectedUpdatedAt":`+strconv.FormatInt(int64(deployed["updatedAt"].(float64)), 10)+`}`)
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status/body = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
	missingService := httptest.NewRecorder()
	handler.ServeHTTP(missingService, projectRequest(http.MethodGet, "/api/v1/projects/project/services/service", ""))
	if missingService.Code != http.StatusNotFound {
		t.Fatalf("deleted service get = %d/%s", missingService.Code, missingService.Body)
	}
}

func TestServiceDeploymentRestartAndRemoveRoutesTargetExactDeployment(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateService(context.Background(), state.CreateService{
		ID: "service", ProjectID: "project", Name: "api", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{ImageReference: "alpine:3.22"},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &serviceDeploymentActionRepository{Store: store}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithServices(repository)),
	)
	for _, action := range []string{"restart", "remove"} {
		request := projectRequest(http.MethodPost, "/api/v1/projects/project/services/service/deployments/deployment/"+action, `{"expectedUpdatedAt":`+strconv.FormatInt(service.UpdatedAtMillis, 10)+`}`)
		request.Header.Set("Origin", "https://admin.example.com")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s deployment = %d/%s", action, response.Code, response.Body)
		}
	}
	if repository.restarted != "deployment" || repository.removed != "deployment" {
		t.Fatalf("deployment actions = restart %q, remove %q", repository.restarted, repository.removed)
	}
}
