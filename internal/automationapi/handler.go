package automationapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/state"
)

type Repository interface {
	Projects(context.Context) ([]state.ProjectSummary, error)
	Project(context.Context, string) (state.ProjectSummary, error)
	ProjectCanvas(context.Context, string) (state.ProjectCanvas, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error)
}

type ManagedImageCatalog interface {
	List(context.Context, managedimages.Engine, int, int, string) (managedimages.Page, error)
}

type Config struct {
	Hostname         string
	Repository       Repository
	Projects         *automation.ProjectApplication
	Services         *automation.ServiceApplication
	Domains          *automation.DomainApplication
	Logs             *automation.LogApplication
	Images           ManagedImageCatalog
	Redis            *automation.ManagedRedisApplication
	RedisStore       managedRedisRepository
	Postgres         *automation.ManagedPostgresApplication
	PostgresStore    managedPostgresRepository
	ObjectStores     objectStoreApplication
	Managed          *automation.ManagedResourceApplication
	Versions         *databaseversion.Service
	ServerExec       *automation.ServerExecApplication
	Volumes          *automation.VolumeApplication
	PortForwards     *portforward.Application
	Registry         registryApplication
	RegistrySettings registrySettings
	Admission        *admission.Gate
}

func Handler(config Config) (http.Handler, error) {
	if config.Hostname == "" || config.Repository == nil || config.Services == nil || config.Logs == nil || config.Images == nil || config.Admission == nil {
		return nil, errors.New("automation API dependencies are incomplete")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/openapi.json", serveOpenAPI(config.Hostname, openAPIFeatures{
		serverExec: config.ServerExec != nil, managedResources: config.Managed != nil,
		databaseVersions: config.Versions != nil, volumes: config.Volumes != nil,
		projects: config.Projects != nil, objectStores: config.ObjectStores != nil,
		domains: config.Domains != nil, registry: config.Registry != nil && config.RegistrySettings != nil,
		portForwards: config.PortForwards != nil,
	}))
	mux.HandleFunc("GET /api/v1/me", serveIdentity)
	mux.HandleFunc("GET /api/v1/projects", listProjects(config.Repository))
	if config.Projects != nil {
		mux.HandleFunc("POST /api/v1/projects", createProject(config.Projects))
	}
	mux.HandleFunc("GET /api/v1/projects/{projectID}", getProject(config.Repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services", listServices(config.Repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}", getService(config.Repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/deployments", listDeployments(config.Repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/logs", readServiceLogs(config.Logs))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services", createService(config.Services))
	mux.HandleFunc("PUT /api/v1/projects/{projectID}/services/{serviceID}", updateService(config.Services))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/redeploy", redeployService(config.Services))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/rollback", rollbackService(config.Services))
	if config.Domains != nil {
		pattern := "/api/v1/projects/{projectID}/services/{serviceID}/domains"
		mux.HandleFunc("GET "+pattern, listServiceDomains(config.Domains))
		mux.HandleFunc("POST "+pattern, attachServiceDomain(config.Domains))
		mux.HandleFunc("DELETE "+pattern+"/{hostname}", detachServiceDomain(config.Domains))
	}
	if config.Volumes != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/volumes", listVolumes(config.Volumes))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/services/{serviceID}/volumes", createVolume(config.Volumes))
		mux.HandleFunc("DELETE /api/v1/projects/{projectID}/services/{serviceID}/volumes/{volumeID}", deleteVolume(config.Volumes))
	}
	if config.PortForwards != nil {
		mux.HandleFunc(
			"POST /api/v1/projects/{projectID}/resources/{kind}/{resourceID}/port-forwards",
			createPortForward(config.Hostname, config.PortForwards),
		)
	}
	if config.Registry != nil && config.RegistrySettings != nil {
		registerRegistryRoutes(mux, config.Registry, config.RegistrySettings)
	}
	mux.HandleFunc("GET /api/v1/managed-images/{engine}/tags", listManagedImageTags(config.Images))
	if config.Redis != nil && config.RedisStore != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/redis", listManagedRedis(config.RedisStore))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}", getManagedRedis(config.RedisStore))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/redis", createManagedRedis(config.Redis))
	}
	if config.Postgres != nil && config.PostgresStore != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres", listManagedPostgres(config.PostgresStore))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/postgres/{postgresID}", getManagedPostgres(config.PostgresStore))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/postgres", createManagedPostgres(config.Postgres))
	}
	if config.ObjectStores != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores", listObjectStores(config.ObjectStores))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/object-stores", createObjectStore(config.ObjectStores))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/object-stores/{storeID}", getObjectStore(config.ObjectStores))
	}
	if config.ServerExec != nil {
		mux.HandleFunc("POST /api/v1/server/exec", executeServerCommand(config.ServerExec))
	}
	if config.Managed != nil {
		mux.HandleFunc("GET /api/v1/projects/{projectID}/managed-resources", listManagedResources(config.Managed))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/managed-resources/{kind}/{resourceID}", getManagedResource(config.Managed))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/managed-resources/{kind}/{resourceID}/backups", readManagedResourceBackups(config.Managed))
	}
	if config.Versions != nil {
		mux.HandleFunc("POST /api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change/preview", previewDatabaseVersionChange(config.Versions))
		mux.HandleFunc("POST /api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change", startDatabaseVersionChange(config.Versions))
		mux.HandleFunc("GET /api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change/{operationID}", readDatabaseVersionChange(config.Versions))
	}
	return noStore(admission.WrapHTTPMutations(config.Admission, "automation_request", "", nil, mux)), nil
}

func serveIdentity(response http.ResponseWriter, request *http.Request) {
	identity, ok := automation.IdentityFromContext(request.Context())
	if !ok {
		writeError(response, http.StatusUnauthorized, "token_identity_required", "Bearer token identity is required")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"tokenId": identity.TokenID, "role": identity.Role, "projectId": identity.ProjectID,
	})
}

func listProjects(repository Repository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireIdentity(response, request)
		if !ok {
			return
		}
		if identity.ProjectID != nil {
			project, err := repository.Project(request.Context(), *identity.ProjectID)
			if err != nil {
				writeRepositoryError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, []projectResponse{publicProject(project)})
			return
		}
		projects, err := repository.Projects(request.Context())
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]projectResponse, 0, len(projects))
		for _, project := range projects {
			result = append(result, publicProject(project))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getProject(repository Repository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !requireProject(response, request, request.PathValue("projectID")) {
			return
		}
		project, err := repository.Project(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicProject(project))
	}
}

func listServices(repository Repository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		canvas, err := repository.ProjectCanvas(request.Context(), projectID)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]serviceSummaryResponse, 0)
		for _, resource := range canvas.Resources {
			if resource.Kind == "service" {
				result = append(result, publicServiceSummary(resource))
			}
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getService(repository Repository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		service, err := repository.Service(request.Context(), projectID, request.PathValue("serviceID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicService(service))
	}
}

func listDeployments(repository Repository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		limit := 0
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_page_size", "limit must be an integer from 1 to 100")
				return
			}
			limit = parsed
		}
		page, err := repository.ServiceDeployments(
			request.Context(), projectID, request.PathValue("serviceID"),
			request.URL.Query().Get("cursor"), limit,
		)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]deploymentResponse, 0, len(page.Deployments))
		for _, deployment := range page.Deployments {
			result = append(result, publicDeployment(deployment))
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"deployments": result, "nextCursor": page.NextCursor,
		})
	}
}

func requireIdentity(response http.ResponseWriter, request *http.Request) (automation.Identity, bool) {
	identity, ok := automation.IdentityFromContext(request.Context())
	if !ok {
		writeError(response, http.StatusUnauthorized, "token_identity_required", "Bearer token identity is required")
	}
	return identity, ok
}

func requireProject(response http.ResponseWriter, request *http.Request, projectID string) bool {
	identity, ok := requireIdentity(response, request)
	if !ok {
		return false
	}
	if !identity.AllowsProject(projectID) {
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
		return false
	}
	return true
}

func writeRepositoryError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrServiceNotFound):
		writeError(response, http.StatusNotFound, "service_not_found", "Service not found")
	case errors.Is(err, state.ErrManagedRedisNotFound):
		writeError(response, http.StatusNotFound, "redis_not_found", "Managed Redis resource not found")
	case errors.Is(err, state.ErrManagedPostgresNotFound):
		writeError(response, http.StatusNotFound, "postgres_not_found", "Managed PostgreSQL resource not found")
	case errors.Is(err, state.ErrObjectStoreNotFound):
		writeError(response, http.StatusNotFound, "object_store_not_found", "Object store not found")
	case errors.Is(err, state.ErrDeploymentPageInvalid), errors.Is(err, state.ErrDeploymentCursorInvalid):
		writeError(response, http.StatusBadRequest, "invalid_deployment_page", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to read platform state")
	}
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		next.ServeHTTP(response, request)
	})
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
