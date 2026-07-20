package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type volumeRepositoryStub struct {
	created []state.Volume
	service state.ServiceDesired
}

func (repository *volumeRepositoryStub) CreateVolume(_ context.Context, input state.CreateVolume) (state.Volume, error) {
	repository.created = append(repository.created, input.Volume)
	return input.Volume, nil
}

func (repository *volumeRepositoryStub) VolumesByService(_ context.Context, projectID, serviceID string) ([]state.Volume, error) {
	result := make([]state.Volume, 0, len(repository.created))
	for _, item := range repository.created {
		if item.ProjectID == projectID && item.ServiceID == serviceID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (repository *volumeRepositoryStub) DeleteVolume(_ context.Context, input state.DeleteVolume) (state.Volume, error) {
	for index, item := range repository.created {
		if item.ID == input.VolumeID && item.ProjectID == input.ProjectID && item.ServiceID == input.ServiceID {
			repository.created = append(repository.created[:index], repository.created[index+1:]...)
			return item, nil
		}
	}
	return state.Volume{}, state.ErrVolumeNotFound
}

func (repository *volumeRepositoryStub) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return repository.service, nil
}

type volumeFilesystemStub struct{}

func (volumeFilesystemStub) Ensure(state.PersistentVolumeReference) error { return nil }
func (volumeFilesystemStub) Remove(string, string) error                  { return nil }

type volumeImageInspectorStub struct{}

func (volumeImageInspectorStub) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{User: "1000:1001"}, nil
}

func TestVolumeAPICreatesListsSuggestsAndDeletes(t *testing.T) {
	repository := &volumeRepositoryStub{service: state.ServiceDesired{ActiveImageDigest: "sha256:image"}}
	application, err := volume.New(volume.Config{
		Repository: repository, Filesystem: volumeFilesystemStub{}, Images: volumeImageInspectorStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithVolumes(application))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	suggestion := projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/volumes/owner-suggestion", "")
	suggestionResponse := httptest.NewRecorder()
	handler.ServeHTTP(suggestionResponse, suggestion)
	if suggestionResponse.Code != http.StatusOK || !strings.Contains(suggestionResponse.Body.String(), `"exactNumeric":true`) ||
		!strings.Contains(suggestionResponse.Body.String(), `"ownerUid":1000`) {
		t.Fatalf("suggestion = %d/%s", suggestionResponse.Code, suggestionResponse.Body)
	}

	create := projectRequest(http.MethodPost, "/api/v1/projects/project/services/service/volumes", `{"name":"data","ownerUid":1000,"ownerGid":1001}`)
	create.Header.Set("Origin", "https://admin.example.com")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Location") == "" ||
		createResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("create = %d/%v/%s", createResponse.Code, createResponse.Header(), createResponse.Body)
	}
	if len(repository.created) != 1 {
		t.Fatalf("created volumes = %+v", repository.created)
	}

	list := projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/volumes", "")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"data"`) {
		t.Fatalf("list = %d/%s", listResponse.Code, listResponse.Body)
	}

	deleteRequest := projectRequest(http.MethodDelete, "/api/v1/projects/project/services/service/volumes/"+repository.created[0].ID, "")
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || deleteResponse.Body.Len() != 0 || deleteResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("delete = %d/%v/%s", deleteResponse.Code, deleteResponse.Header(), deleteResponse.Body)
	}
	if len(repository.created) != 0 {
		t.Fatalf("volume survived delete: %+v", repository.created)
	}

	direct := projectRequest(http.MethodGet, "/api/v1/projects/project/services/service/volumes", "")
	directResponse := httptest.NewRecorder()
	raw.ServeHTTP(directResponse, direct)
	if directResponse.Code != http.StatusForbidden {
		t.Fatalf("unprotected list status = %d", directResponse.Code)
	}
}

func TestVolumeAPIRejectsUnknownFields(t *testing.T) {
	repository := &volumeRepositoryStub{}
	application, err := volume.New(volume.Config{
		Repository: repository, Filesystem: volumeFilesystemStub{}, Images: volumeImageInspectorStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(server.DefaultMeta("ready"), server.WithVolumes(application)),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services/service/volumes", `{"name":"data","ownerUid":0,"ownerGid":0,"readOnly":true}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(repository.created) != 0 {
		t.Fatalf("unknown field = %d/%s", response.Code, response.Body)
	}
}

func TestServiceAPICreatesInitialDomainsListenersAndMountedVolumes(t *testing.T) {
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

	volumeApplication, err := volume.New(volume.Config{
		Repository: store, Filesystem: volumeFilesystemStub{}, Images: volumeImageInspectorStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(
			server.DefaultMeta("ready"), server.WithServices(store), server.WithDomains(store),
			server.WithServiceListeners(store), server.WithVolumes(volumeApplication),
		),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services", `{
  "name":"api",
  "enabled":false,
  "source":{"type":"public_image","autoUpdate":true,"image":{"reference":"nginx:stable"}},
  "domains":[{"hostname":"api.example.com","targetPort":8080}],
  "listeners":[{"protocol":"tcp","publicPort":9000,"targetPort":8080}],
  "volumes":[{"name":"data","ownerUid":1000,"ownerGid":1001,"containerPath":"/data"}]
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d/%s", response.Code, response.Body)
	}

	serviceID := strings.TrimPrefix(response.Header().Get("Location"), "/api/v1/projects/project/services/")
	created, err := store.Service(context.Background(), "project", serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Enabled || len(created.Snapshot.VolumeMounts) != 1 || created.Snapshot.VolumeMounts[0].ContainerPath != "/data" {
		t.Fatalf("created service = %+v", created)
	}
	volumes, err := store.VolumesByService(context.Background(), "project", serviceID)
	if err != nil || len(volumes) != 1 || created.Snapshot.VolumeMounts[0].VolumeID != volumes[0].ID {
		t.Fatalf("created volumes/mounts = %+v/%+v, %v", volumes, created.Snapshot.VolumeMounts, err)
	}
	domains, err := store.ServiceDomains(context.Background(), "project", serviceID)
	if err != nil || len(domains) != 1 || domains[0].Hostname != "api.example.com" || domains[0].TargetPort != 8080 {
		t.Fatalf("domains = %+v, %v", domains, err)
	}
	listeners, err := store.ServiceListeners(context.Background(), "project", serviceID)
	if err != nil || len(listeners) != 1 || listeners[0].Protocol != "tcp" || listeners[0].PublicPort != 9000 || listeners[0].TargetPort != 8080 {
		t.Fatalf("listeners = %+v, %v", listeners, err)
	}
}

func TestServiceAPIRollsBackIncompleteInitialSetup(t *testing.T) {
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
	volumeApplication, err := volume.New(volume.Config{
		Repository: store, Filesystem: volumeFilesystemStub{}, Images: volumeImageInspectorStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := access.ProtectAdmin(
		"admin.example.com", projectVerifier{},
		server.Handler(
			server.DefaultMeta("ready"), server.WithServices(store), server.WithDomains(store),
			server.WithVolumes(volumeApplication),
		),
	)
	request := projectRequest(http.MethodPost, "/api/v1/projects/project/services", `{
  "name":"api",
  "source":{"type":"public_image","autoUpdate":true,"image":{"reference":"nginx:stable"}},
  "domains":[{"hostname":"api.example.com","targetPort":0}],
  "volumes":[{"name":"data","ownerUid":1000,"ownerGid":1001,"containerPath":"/data"}]
}`)
	request.Header.Set("Origin", "https://admin.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_domain"`) {
		t.Fatalf("create = %d/%s", response.Code, response.Body)
	}
	var services, volumes int
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM services WHERE project_id = ?", "project").Scan(&services); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRowContext(context.Background(), "SELECT count(*) FROM volumes WHERE project_id = ?", "project").Scan(&volumes); err != nil {
		t.Fatal(err)
	}
	if services != 0 || volumes != 0 {
		t.Fatalf("incomplete service survived rollback: services=%d volumes=%d", services, volumes)
	}
}
