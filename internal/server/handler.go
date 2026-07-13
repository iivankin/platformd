package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/terminalauth"
	"github.com/iivankin/platformd/internal/ui"
	"github.com/iivankin/platformd/internal/version"
)

type Meta struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Status       string `json:"status"`
	Version      string `json:"version"`
}

type handlerConfig struct {
	projects           ProjectRepository
	services           ServiceRepository
	domains            DomainRepository
	tokens             APITokenRepository
	imageCredentials   ImageCredentialRepository
	logs               LogRepository
	logsHostname       string
	audit              AuditRepository
	managedImages      ManagedImageCatalog
	managedRedis       ManagedRedisRepository
	managedPostgres    *managedpostgres.Application
	objectStores       *objectstore.Application
	registry           *registry.Application
	registrySettings   RegistrySettings
	backupTargets      *backup.TargetApplication
	backupResources    *backup.ResourceApplication
	databaseVersions   *databaseversion.Service
	containerConsole   ContainerConsole
	serverTerminalAuth *terminalauth.Service
	adminHostname      string
	diskPressure       DiskPressure
	admission          *admission.Gate
	selfUpdater        SelfUpdater
	afterUpdate        func()
	recovery           RecoveryRepository
	random             io.Reader
	now                func() time.Time
}

type Option func(*handlerConfig)

func WithProjects(repository ProjectRepository) Option {
	return func(config *handlerConfig) {
		config.projects = repository
	}
}

func WithImageCredentials(repository ImageCredentialRepository) Option {
	return func(config *handlerConfig) {
		config.imageCredentials = repository
	}
}

func WithServices(repository ServiceRepository) Option {
	return func(config *handlerConfig) {
		config.services = repository
	}
}

func WithDomains(repository DomainRepository) Option {
	return func(config *handlerConfig) {
		config.domains = repository
	}
}

func WithAPITokens(repository APITokenRepository) Option {
	return func(config *handlerConfig) {
		config.tokens = repository
	}
}

func WithLogs(hostname string, repository LogRepository) Option {
	return func(config *handlerConfig) {
		config.logs = repository
		config.logsHostname = hostname
	}
}

func WithAudit(repository AuditRepository) Option {
	return func(config *handlerConfig) {
		config.audit = repository
	}
}

func WithManagedImages(catalog ManagedImageCatalog) Option {
	return func(config *handlerConfig) {
		config.managedImages = catalog
	}
}

func WithManagedRedis(repository ManagedRedisRepository) Option {
	return func(config *handlerConfig) {
		config.managedRedis = repository
	}
}

func WithManagedPostgres(application *managedpostgres.Application) Option {
	return func(config *handlerConfig) {
		config.managedPostgres = application
	}
}

func WithObjectStores(application *objectstore.Application) Option {
	return func(config *handlerConfig) {
		config.objectStores = application
	}
}

func WithRegistry(application *registry.Application, settings RegistrySettings) Option {
	return func(config *handlerConfig) {
		config.registry = application
		config.registrySettings = settings
	}
}

func WithBackupTargets(application *backup.TargetApplication) Option {
	return func(config *handlerConfig) {
		config.backupTargets = application
	}
}

func WithBackupResources(application *backup.ResourceApplication) Option {
	return func(config *handlerConfig) {
		config.backupResources = application
	}
}

func WithDatabaseVersions(service *databaseversion.Service) Option {
	return func(config *handlerConfig) {
		config.databaseVersions = service
	}
}

func WithContainerConsole(hostname string, application ContainerConsole) Option {
	return func(config *handlerConfig) {
		config.adminHostname = hostname
		config.containerConsole = application
	}
}

func WithServerTerminalAuth(service *terminalauth.Service) Option {
	return func(config *handlerConfig) {
		config.serverTerminalAuth = service
	}
}

func WithDiskPressure(pressure DiskPressure) Option {
	return func(config *handlerConfig) {
		config.diskPressure = pressure
	}
}

func WithAdmission(gate *admission.Gate) Option {
	return func(config *handlerConfig) {
		config.admission = gate
	}
}

func WithSelfUpdate(updater SelfUpdater, afterCommit func()) Option {
	return func(config *handlerConfig) {
		config.selfUpdater = updater
		config.afterUpdate = afterCommit
	}
}

func WithRecovery(repository RecoveryRepository) Option {
	return func(config *handlerConfig) {
		config.recovery = repository
	}
}

func Handler(meta Meta, options ...Option) http.Handler {
	config := handlerConfig{random: rand.Reader, now: time.Now}
	for _, option := range options {
		option(&config)
	}
	static := newSPAHandler(ui.Files())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/v1/meta", handleMeta(meta))
	mux.HandleFunc("GET /api/v1/me", handleIdentity)
	if config.projects != nil {
		registerProjectRoutes(mux, config)
	}
	if config.imageCredentials != nil {
		registerImageCredentialRoutes(mux, config)
	}
	if config.services != nil {
		registerServiceRoutes(mux, config)
	}
	if config.domains != nil {
		registerServiceDomainRoutes(mux, config)
	}
	if config.tokens != nil {
		registerAPITokenRoutes(mux, config)
	}
	if config.logs != nil {
		if err := registerLogRoutes(mux, config.logsHostname, config.logs); err != nil {
			panic("register log routes: " + err.Error())
		}
	}
	if config.audit != nil {
		registerAuditRoutes(mux, config.audit)
	}
	if config.managedImages != nil {
		registerManagedImageRoutes(mux, config.managedImages)
	}
	if config.managedRedis != nil {
		registerManagedRedisRoutes(mux, config.managedRedis)
	}
	if config.managedPostgres != nil {
		registerManagedPostgresRoutes(mux, config.managedPostgres)
	}
	if config.objectStores != nil {
		registerObjectStoreRoutes(mux, config.objectStores)
	}
	if config.registry != nil && config.registrySettings != nil {
		registerRegistryRoutes(mux, config)
	}
	if config.backupTargets != nil {
		registerBackupTargetRoutes(mux, config.backupTargets)
	}
	if config.backupResources != nil {
		registerBackupResourceRoutes(mux, config.backupResources)
	}
	if config.databaseVersions != nil {
		registerDatabaseVersionRoutes(mux, config.databaseVersions)
	}
	if config.containerConsole != nil {
		if err := registerContainerConsoleRoute(mux, config.adminHostname, config.containerConsole, config.admission); err != nil {
			panic("register container console: " + err.Error())
		}
	}
	if config.serverTerminalAuth != nil {
		registerServerTerminalAuthRoute(mux, config.serverTerminalAuth)
	}
	if config.diskPressure != nil {
		registerInfrastructureRoutes(mux, config.diskPressure)
	}
	if config.selfUpdater != nil && config.afterUpdate != nil {
		registerSelfUpdateRoute(mux, config.selfUpdater, config.afterUpdate)
	}
	if config.recovery != nil {
		registerRecoveryRoutes(mux, config.recovery)
	}
	mux.Handle("/", static)
	var handler http.Handler = mux
	if config.admission != nil {
		handler = admission.WrapHTTPMutations(config.admission, "admin_request", "/api/v1/infrastructure/update", handler)
	}
	return securityHeaders(handler)
}

func handleHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func DefaultMeta(status string) Meta {
	return Meta{Architecture: runtime.GOARCH, OS: runtime.GOOS, Status: status, Version: version.Version}
}

func handleMeta(meta Meta) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(meta)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self' data: wss:; font-src 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

type spaHandler struct {
	files      fs.FS
	fileServer http.Handler
	index      []byte
}

func newSPAHandler(files fs.FS) http.Handler {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic("read embedded UI index: " + err.Error())
	}
	return &spaHandler{files: files, fileServer: http.FileServerFS(files), index: index}
}

func (handler *spaHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(request.URL.Path, "/")
	if path == "" || path == "index.html" {
		handler.serveIndex(response, request)
		return
	}
	if _, err := fs.Stat(handler.files, path); err == nil {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		handler.fileServer.ServeHTTP(response, request)
		return
	}

	if strings.HasPrefix(request.URL.Path, "/api/") {
		http.NotFound(response, request)
		return
	}

	handler.serveIndex(response, request)
}

func (handler *spaHandler) serveIndex(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Length", fmt.Sprintf("%d", len(handler.index)))
	if request.Method == http.MethodHead {
		return
	}
	_, _ = response.Write(handler.index)
}
