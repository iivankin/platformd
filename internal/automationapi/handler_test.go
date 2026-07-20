package automationapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type repositoryStub struct {
	projects      []state.ProjectSummary
	projectCalls  int
	projectsCalls int
	canvasCalls   int
	createCalls   int
	created       state.CreateService
	serviceCalls  int
	service       state.ServiceDesired
	volumes       []state.Volume
	volumeCreate  state.CreateVolume
	projectCreate state.CreateProjectByToken
	objectCreate  objectstore.CreateInput
	objectStores  []state.ObjectStore
	domains       []state.ServiceDomain
	domainAttach  state.AttachServiceDomainInput
	domainDetach  state.DetachServiceDomainInput
}

func (*repositoryStub) Resource(context.Context, string, string, string) error { return nil }

func (*repositoryStub) ResolveResourceAddress(string, string, string, int) (string, error) {
	return "10.42.0.4:5432", nil
}

func (*repositoryStub) RecordPortForwardTicket(context.Context, portforward.AuditRecord) error {
	return nil
}

func (repository *repositoryStub) ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error) {
	return repository.domains, nil
}

func (repository *repositoryStub) AttachServiceDomain(_ context.Context, input state.AttachServiceDomainInput) (state.ServiceDomain, error) {
	repository.domainAttach = input
	domain := state.ServiceDomain{
		Hostname: input.Hostname, ProjectID: input.ProjectID, ProjectName: "shop",
		ServiceID: input.ServiceID, ServiceName: "api", CreatedAt: input.CreatedAtMillis,
	}
	repository.domains = append(repository.domains, domain)
	return domain, nil
}

func (repository *repositoryStub) DetachServiceDomain(_ context.Context, input state.DetachServiceDomainInput) error {
	repository.domainDetach = input
	repository.domains = nil
	return nil
}

func (repository *repositoryStub) CreateProjectByToken(_ context.Context, input state.CreateProjectByToken) (state.ProjectSummary, error) {
	repository.projectCreate = input
	project := state.ProjectSummary{ID: input.ID, Name: input.Name, CreatedAtMillis: input.CreatedAtMillis, UpdatedAtMillis: input.CreatedAtMillis}
	repository.projects = append(repository.projects, project)
	return project, nil
}

func (repository *repositoryStub) Stores(context.Context, string) ([]state.ObjectStore, error) {
	return repository.objectStores, nil
}

func (repository *repositoryStub) Store(_ context.Context, projectID, storeID string) (state.ObjectStore, error) {
	for _, store := range repository.objectStores {
		if store.ProjectID == projectID && store.ID == storeID {
			return store, nil
		}
	}
	return state.ObjectStore{}, state.ErrObjectStoreNotFound
}

func (repository *repositoryStub) Create(_ context.Context, input objectstore.CreateInput) (objectstore.CreateResult, error) {
	repository.objectCreate = input
	store := state.ObjectStore{
		ID: "store", ProjectID: input.ProjectID, ProjectName: "shop", Name: input.Name,
		BucketName: input.BucketName, PublicHostname: input.PublicHostname,
		CORSOrigins: input.CORSOrigins, CreatedAtMillis: 42, UpdatedAtMillis: 42,
	}
	repository.objectStores = append(repository.objectStores, store)
	return objectstore.CreateResult{
		Store: store, Credential: state.S3Credential{Permission: "read_write"},
		AccessKey: "access-key", Secret: "one-time-secret", RequestID: "object-request",
	}, nil
}

func (repository *repositoryStub) CreateService(_ context.Context, input state.CreateService) (state.ServiceDesired, error) {
	repository.createCalls++
	repository.created = input
	return state.ServiceDesired{
		ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, Enabled: input.Enabled,
		Snapshot: input.Snapshot, CreatedAtMillis: input.CreatedAtMillis, UpdatedAtMillis: input.CreatedAtMillis,
	}, nil
}

func (*repositoryStub) UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*repositoryStub) List(context.Context, managedimages.Engine, int, int) (managedimages.Page, error) {
	return managedimages.Page{Tags: []managedimages.Tag{{Name: "18.3"}}, Page: 1, PageSize: 50}, nil
}

func automationHandler(t *testing.T, repository *repositoryStub) http.Handler {
	t.Helper()
	projects, err := automation.NewProjectApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	services, err := automation.NewServiceApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	domains, err := automation.NewDomainApplication(repository, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := automation.NewLogApplication(repository, logReaderStub{})
	if err != nil {
		t.Fatal(err)
	}
	volumeDomain, err := volume.New(volume.Config{
		Repository: repository, Filesystem: automationVolumeFilesystem{}, Images: automationVolumeImages{},
	})
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := automation.NewVolumeApplication(volumeDomain)
	if err != nil {
		t.Fatal(err)
	}
	portForwards, err := portforward.New(portforward.Config{
		Repository: repository, Resolver: repository, Audit: repository,
		NewID: func() (string, error) { return "port-forward-id", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(Config{
		Hostname: "api.example.com", Repository: repository, Projects: projects,
		Services: services, Domains: domains, Logs: logs, Images: repository, ObjectStores: repository,
		Volumes: volumes, PortForwards: portForwards, Admission: admission.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type automationVolumeFilesystem struct{}

func (automationVolumeFilesystem) Ensure(state.PersistentVolumeReference) error { return nil }
func (automationVolumeFilesystem) Remove(string, string) error                  { return nil }

type automationVolumeImages struct{}

func (automationVolumeImages) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, nil
}

func TestAutomationAPIListsOfficialManagedImageTags(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/managed-images/postgres/tags?page=1&pageSize=50&search=18", automation.Identity{TokenID: "token", Role: "read"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"18.3"`) {
		t.Fatalf("managed image tags = %d/%s", response.Code, response.Body)
	}
}

func (repository *repositoryStub) Projects(context.Context) ([]state.ProjectSummary, error) {
	repository.projectsCalls++
	return repository.projects, nil
}

func (repository *repositoryStub) Project(_ context.Context, projectID string) (state.ProjectSummary, error) {
	repository.projectCalls++
	for _, project := range repository.projects {
		if project.ID == projectID {
			return project, nil
		}
	}
	return state.ProjectSummary{}, state.ErrProjectNotFound
}

func (repository *repositoryStub) ProjectCanvas(context.Context, string) (state.ProjectCanvas, error) {
	repository.canvasCalls++
	return state.ProjectCanvas{}, nil
}

func (repository *repositoryStub) Service(_ context.Context, projectID, serviceID string) (state.ServiceDesired, error) {
	repository.serviceCalls++
	if repository.service.ProjectID == projectID && repository.service.ID == serviceID {
		return repository.service, nil
	}
	return state.ServiceDesired{}, state.ErrServiceNotFound
}

func (repository *repositoryStub) CreateVolume(_ context.Context, input state.CreateVolume) (state.Volume, error) {
	repository.volumeCreate = input
	repository.volumes = append(repository.volumes, input.Volume)
	return input.Volume, nil
}

func (repository *repositoryStub) VolumesByService(context.Context, string, string) ([]state.Volume, error) {
	return repository.volumes, nil
}

func (repository *repositoryStub) DeleteVolume(_ context.Context, input state.DeleteVolume) (state.Volume, error) {
	for index, item := range repository.volumes {
		if item.ID == input.VolumeID {
			repository.volumes = append(repository.volumes[:index], repository.volumes[index+1:]...)
			return item, nil
		}
	}
	return state.Volume{}, state.ErrVolumeNotFound
}

type logReaderStub struct{}

func (logReaderStub) Read(context.Context, containerlogs.Query) (containerlogs.Window, error) {
	return containerlogs.Window{Records: []containerlogs.Record{{Text: "ready"}}}, nil
}

func (*repositoryStub) ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error) {
	return state.DeploymentPage{}, nil
}

func TestAutomationAPIEnforcesProjectBoundaryBeforeLookup(t *testing.T) {
	repository := &repositoryStub{projects: []state.ProjectSummary{
		{ID: "project-a", Name: "alpha"}, {ID: "project-b", Name: "beta"},
	}}
	handler := automationHandler(t, repository)
	projectID := "project-a"
	identity := automation.Identity{TokenID: "token", Role: "read", ProjectID: &projectID}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects", identity))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"project-a"`) || strings.Contains(response.Body.String(), "project-b") {
		t.Fatalf("bounded project list = %d/%s", response.Code, response.Body)
	}
	if repository.projectCalls != 1 || repository.projectsCalls != 0 {
		t.Fatalf("bounded list used global lookup: exact=%d global=%d", repository.projectCalls, repository.projectsCalls)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-b/services", identity))
	if response.Code != http.StatusForbidden || repository.canvasCalls != 0 {
		t.Fatalf("cross-project request = %d/%s, lookups=%d", response.Code, response.Body, repository.canvasCalls)
	}
}

func TestAutomationAPIPublishesOpenAPIAndRequiresIdentity(t *testing.T) {
	handler := automationHandler(t, &repositoryStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/projects", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/openapi.json", automation.Identity{TokenID: "token", Role: "read"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"openapi":"3.1.0"`) || !strings.Contains(response.Body.String(), `"url":"https://api.example.com"`) || !strings.Contains(response.Body.String(), `/volumes`) || !strings.Contains(response.Body.String(), `/object-stores`) || !strings.Contains(response.Body.String(), `/domains`) || !strings.Contains(response.Body.String(), `ProjectCreateRequest`) || strings.Contains(response.Body.String(), `/query`) {
		t.Fatalf("OpenAPI response = %d/%s", response.Code, response.Body)
	}
}

func TestAutomationAPIManagesServiceDomainsWithinTokenBoundary(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects/project/services/service/domains"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"hostname":"app.example.com","targetPort":8080,"move":true}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "admin", Role: "admin"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.domainAttach.ActorKind != "token" || repository.domainAttach.ActorID != "admin" || repository.domainAttach.TargetPort != 8080 || !repository.domainAttach.Move {
		t.Fatalf("attach domain = %d/%s input=%+v", response.Code, response.Body, repository.domainAttach)
	}
	bound := "other"
	request = httptest.NewRequest(http.MethodDelete, path+"/app.example.com", nil)
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "bound", Role: "admin", ProjectID: &bound}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || repository.domainDetach.Hostname != "" {
		t.Fatalf("cross-project detach = %d/%s input=%+v", response.Code, response.Body, repository.domainDetach)
	}
	request = httptest.NewRequest(http.MethodDelete, path+"/app.example.com", nil)
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "admin", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || repository.domainDetach.ActorKind != "token" || repository.domainDetach.ActorID != "admin" {
		t.Fatalf("detach domain = %d/%s input=%+v", response.Code, response.Body, repository.domainDetach)
	}
}

func TestAutomationAPICreatesProjectOnlyWithUnboundAdmin(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects"
	bound := "project"
	for _, identity := range []automation.Identity{
		{TokenID: "read", Role: "read"},
		{TokenID: "bound", Role: "admin", ProjectID: &bound},
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-json"))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(automation.WithIdentity(request.Context(), identity))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || repository.projectCreate.ID != "" {
			t.Fatalf("denied project create = %d/%s input=%+v", response.Code, response.Body, repository.projectCreate)
		}
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"shop"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "root-token", Role: "admin"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.projectCreate.ActorTokenID != "root-token" || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("project create = %d/%s input=%+v", response.Code, response.Body, repository.projectCreate)
	}
}

func TestAutomationAPIManagesObjectStoreMetadataWithoutObjectDataRoutes(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects/project/object-stores"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"assets","bucketName":"assets-bucket"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "admin", Role: "admin"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.objectCreate.Actor.Kind != "token" || repository.objectCreate.Actor.ID != "admin" || !strings.Contains(response.Body.String(), `"secret":"one-time-secret"`) {
		t.Fatalf("object store create = %d/%s input=%+v", response.Code, response.Body, repository.objectCreate)
	}
	list := automationRequest("/api/v1/projects/project/object-stores", automation.Identity{TokenID: "read", Role: "read"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "one-time-secret") || !strings.Contains(response.Body.String(), `"region":"us-east-1"`) {
		t.Fatalf("object store list = %d/%s", response.Code, response.Body)
	}
	dataRequest := automationRequest("/api/v1/projects/project/object-stores/store/objects", automation.Identity{TokenID: "admin", Role: "admin"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, dataRequest)
	if response.Code != http.StatusNotFound {
		t.Fatalf("object data route unexpectedly exposed: %d/%s", response.Code, response.Body)
	}
}

func TestAutomationAPIVolumesRespectReadAndAdminRoles(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects/project/services/service/volumes"

	readRequest := httptest.NewRequest(http.MethodGet, path, nil)
	readRequest = readRequest.WithContext(automation.WithIdentity(readRequest.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK || readResponse.Body.String() != "[]\n" {
		t.Fatalf("read volumes = %d/%s", readResponse.Code, readResponse.Body)
	}

	denied := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"data","ownerUid":0,"ownerGid":0}`))
	denied.Header.Set("Content-Type", "application/json")
	denied = denied.WithContext(automation.WithIdentity(denied.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden || len(repository.volumes) != 0 {
		t.Fatalf("read create = %d/%s", deniedResponse.Code, deniedResponse.Body)
	}

	create := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"data","ownerUid":1000,"ownerGid":1001}`))
	create.Header.Set("Content-Type", "application/json")
	create = create.WithContext(automation.WithIdentity(create.Context(), automation.Identity{TokenID: "admin", Role: "admin"}))
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || len(repository.volumes) != 1 || repository.volumeCreate.ActorID != "admin" {
		t.Fatalf("admin create = %d/%s state=%+v", createResponse.Code, createResponse.Body, repository.volumeCreate)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, path+"/"+repository.volumes[0].ID, nil)
	deleteRequest = deleteRequest.WithContext(automation.WithIdentity(deleteRequest.Context(), automation.Identity{TokenID: "admin", Role: "admin"}))
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || len(repository.volumes) != 0 {
		t.Fatalf("admin delete = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
}

func TestAutomationAPIReadsLogsWithinTokenProjectBoundary(t *testing.T) {
	repository := &repositoryStub{service: state.ServiceDesired{ID: "service", ProjectID: "project-a"}}
	handler := automationHandler(t, repository)
	bound := "project-a"
	identity := automation.Identity{TokenID: "token", Role: "read", ProjectID: &bound}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-b/services/service/logs", identity))
	if response.Code != http.StatusForbidden || repository.serviceCalls != 0 {
		t.Fatalf("cross-project logs = %d/%s calls=%d", response.Code, response.Body, repository.serviceCalls)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, automationRequest("/api/v1/projects/project-a/services/service/logs?limit=10", identity))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"text":"ready"`) || repository.serviceCalls != 1 {
		t.Fatalf("visible logs = %d/%s calls=%d", response.Code, response.Body, repository.serviceCalls)
	}
}

func TestAutomationAPIRequiresAdminBeforeDecodingAndCreatesTokenActor(t *testing.T) {
	repository := &repositoryStub{}
	handler := automationHandler(t, repository)
	path := "https://api.example.com/api/v1/projects/project/services"

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-json"))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "read", Role: "read"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || repository.createCalls != 0 {
		t.Fatalf("read mutation = %d/%s, calls=%d", response.Code, response.Body, repository.createCalls)
	}

	request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{
  "name":"api",
  "configuration":{"source":{"type":"public_image","autoUpdate":true,"image":{"reference":"alpine:3.22"}}}
}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(automation.WithIdentity(request.Context(), automation.Identity{TokenID: "admin-token", Role: "admin"}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.createCalls != 1 {
		t.Fatalf("admin mutation = %d/%s, calls=%d", response.Code, response.Body, repository.createCalls)
	}
	if repository.created.ActorKind != "token" || repository.created.ActorID != "admin-token" || repository.created.ActorEmail != "" {
		t.Fatalf("mutation actor = %+v", repository.created)
	}
}

func automationRequest(path string, identity automation.Identity) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.com"+path, nil)
	return request.WithContext(automation.WithIdentity(request.Context(), identity))
}
