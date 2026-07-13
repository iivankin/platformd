package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/mcp"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/sdnotify"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/version"
	"golang.org/x/net/netutil"
)

const shutdownTimeout = 120 * time.Second
const maximumHTTPSConnections = 4096
const managedImageCatalogTimeout = 10 * time.Second

func Run(ctx context.Context) error {
	if os.Getenv("PLATFORMD_DEV") == "1" {
		return runDevelopment(ctx)
	}
	return runProduction(ctx, layout.Production())
}

func runDevelopment(ctx context.Context) error {
	address := os.Getenv("PLATFORMD_DEV_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	return serve(ctx, &http.Server{
		Addr:              address,
		Handler:           server.Handler(server.DefaultMeta("bootstrapping")),
		ReadHeaderTimeout: 5 * time.Second,
	})
}

func runProduction(ctx context.Context, paths layout.Paths) (returnErr error) {
	lock, err := singletonlock.Acquire(paths.DaemonLock, 0)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Close())
	}()
	cgroups, err := cgrouptree.Setup()
	if err != nil {
		return fmt.Errorf("configure delegated cgroups: %w", err)
	}
	key, err := masterkey.Load(paths.MasterKey, 0)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	store, err := state.Open(ctx, paths.StateDatabase, 0)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.MarkInterrupted(ctx, time.Now().UnixMilli()); err != nil {
		return err
	}
	auditCleanupContext, cancelAuditCleanup := context.WithCancel(ctx)
	defer cancelAuditCleanup()
	startAuditCleanup(auditCleanupContext, store)
	installation, err := store.Installation(ctx)
	if err != nil {
		return err
	}
	projects, err := store.RuntimeProjects(ctx)
	if err != nil {
		return err
	}
	runtime, err := startRuntime(ctx, paths, cgroups.WorkloadRoot(), projects)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, runtime.Close())
	}()
	imageCredentials := liveImageCredentialRepository{store: store, master: key}
	registryHostname := ""
	if installation.RegistryHostname != nil {
		registryHostname = *installation.RegistryHostname
	}
	runtime.SetEmbeddedRegistryHost(registryHostname)
	registryPayloads, err := registry.NewPayloadStore(paths.RegistryRoot)
	if err != nil {
		return fmt.Errorf("configure registry payload storage: %w", err)
	}
	registryApplication, err := registry.NewApplication(store, registryPayloads, key, runtime, nil, nil)
	if err != nil {
		return err
	}
	if _, err := registryApplication.CleanupExpiredUploads(ctx); err != nil {
		return fmt.Errorf("clean expired registry uploads: %w", err)
	}
	startRegistryUploadCleanup(ctx, registryApplication)
	backupTargets, err := backup.NewTargetApplication(store, key, backup.NewGate(), nil, nil, nil)
	if err != nil {
		return err
	}
	if err := runtime.ConfigureManagedPostgres(ctx, store, key); err != nil {
		return fmt.Errorf("configure managed PostgreSQL: %w", err)
	}
	if err := runtime.ConfigureManagedRedis(ctx, store, key); err != nil {
		return fmt.Errorf("configure managed Redis: %w", err)
	}
	if err := runtime.ConfigureDeployments(ctx, store, imageCredentials, registryApplication); err != nil {
		return fmt.Errorf("configure service deployments: %w", err)
	}
	if err := runtime.ConfigureServiceWatcher(ctx, store, registryHostname); err != nil {
		return fmt.Errorf("configure service image watcher: %w", err)
	}
	certificates, err := origin.Load(key, installation.OriginCertificates)
	if err != nil {
		return err
	}
	objectPayloads, err := objectstore.NewPayloadStore(paths.ObjectsRoot, key, nil)
	if err != nil {
		return fmt.Errorf("configure encrypted S3 payload storage: %w", err)
	}
	objectStoreRepository := &liveObjectStoreRepository{
		store: store, runtime: runtime, certificates: certificates,
	}
	objectStoreApplication, err := objectstore.NewApplication(objectStoreRepository, objectPayloads, key, nil, nil)
	if err != nil {
		return err
	}
	objectStoreHandler, err := objectstore.NewHTTPHandler(objectstore.HTTPConfig{
		Application: objectStoreApplication,
		LookupHost:  store.ObjectStoreByHostname,
	})
	if err != nil {
		return err
	}
	if err := runtime.ConfigureObjectStores(ctx, store, objectStoreHandler); err != nil {
		return fmt.Errorf("configure managed S3: %w", err)
	}
	registryHandler, err := registry.NewHTTPHandler(registryApplication, automationauth.NewInMemoryFailureLimiter())
	if err != nil {
		return err
	}
	verifier, err := access.New(access.Config{
		TeamDomain: installation.AccessTeamDomain,
		Audience:   installation.AccessAudience,
	})
	if err != nil {
		return fmt.Errorf("configure Cloudflare Access: %w", err)
	}
	tokenVerifier, err := apitoken.NewVerifier(key)
	if err != nil {
		return err
	}
	apiTokens := liveAPITokenRepository{store: store, verifier: tokenVerifier}
	logReader, err := containerlogs.NewReader(paths.LogsRoot)
	if err != nil {
		return err
	}
	logs := liveLogRepository{store: store, reader: logReader}
	managedImageCatalog, err := managedimages.New("https://hub.docker.com", &http.Client{
		Timeout: managedImageCatalogTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	if err != nil {
		return err
	}
	managedRedisApplication, err := managedredis.NewApplication(store, runtime, key, nil, nil)
	if err != nil {
		return err
	}
	managedPostgresApplication, err := managedpostgres.NewApplication(store, runtime, key, nil, nil)
	if err != nil {
		return err
	}
	registrySettings := &liveRegistrySettings{store: store, runtime: runtime, certificates: certificates}
	var automationHostname string
	var automationHandler http.Handler
	if installation.AutomationHostname != nil {
		automationHostname = *installation.AutomationHostname
		automationRepository := liveAutomationRepository{store: store, runtime: runtime}
		serviceAutomation, err := automation.NewServiceApplication(automationRepository, nil, nil)
		if err != nil {
			return err
		}
		redisAutomation, err := automation.NewManagedRedisApplication(managedRedisApplication)
		if err != nil {
			return err
		}
		postgresAutomation, err := automation.NewManagedPostgresApplication(managedPostgresApplication)
		if err != nil {
			return err
		}
		logAutomation, err := automation.NewLogApplication(store, logReader)
		if err != nil {
			return err
		}
		automationAPI, err := automationapi.Handler(automationapi.Config{
			Hostname: automationHostname, Repository: automationRepository, Services: serviceAutomation,
			Logs: logAutomation, Images: managedImageCatalog, Redis: redisAutomation,
			RedisStore: automationRepository, Postgres: postgresAutomation,
			PostgresStore: automationRepository,
		})
		if err != nil {
			return err
		}
		mcpHandler, err := mcp.New(mcp.Config{
			Hostname: automationHostname, Version: version.Version, Repository: automationRepository,
			Services: serviceAutomation, Logs: logAutomation, Images: managedImageCatalog,
			Redis: redisAutomation, Postgres: postgresAutomation,
		})
		if err != nil {
			return err
		}
		authenticator, err := automationauth.New(automationauth.Config{
			Store: store, Verifier: tokenVerifier,
			Limiter: automationauth.NewInMemoryFailureLimiter(),
		})
		if err != nil {
			return err
		}
		automationMux := http.NewServeMux()
		automationMux.Handle("/mcp", mcpHandler)
		automationMux.Handle("/", automationAPI)
		automationHandler = authenticator.Protect(automationMux)
	}
	tlsConfig := certificates.TLSConfig()
	domains := &liveDomainRepository{store: store, certificates: certificates}
	adminHandler := access.ProtectAdmin(
		installation.AdminHostname,
		verifier,
		server.Handler(
			server.DefaultMeta(status(installation.RecoveryMode)),
			server.WithProjects(liveProjectRepository{store: store, runtime: runtime}),
			server.WithServices(liveServiceRepository{store: store, runtime: runtime}),
			server.WithImageCredentials(imageCredentials),
			server.WithDomains(domains),
			server.WithAPITokens(apiTokens),
			server.WithLogs(logs),
			server.WithAudit(store),
			server.WithManagedImages(managedImageCatalog),
			server.WithManagedRedis(managedRedisApplication),
			server.WithManagedPostgres(managedPostgresApplication),
			server.WithObjectStores(objectStoreApplication),
			server.WithRegistry(registryApplication, registrySettings),
			server.WithBackupTargets(backupTargets),
		),
	)
	ingressRouter, err := ingress.New(ingress.Config{
		AdminHostname: installation.AdminHostname, AdminHandler: adminHandler,
		AutomationHostname: automationHostname, AutomationHandler: automationHandler,
		RegistryHostname: registryHostname, RegistryHandler: registryHandler,
		ObjectStoreHandler: objectStoreHandler,
		Backends:           runtime,
	})
	if err != nil {
		return fmt.Errorf("configure HTTPS ingress: %w", err)
	}
	domains.router = ingressRouter
	objectStoreRepository.router = ingressRouter
	registrySettings.router = ingressRouter
	if err := domains.reload(ctx); err != nil {
		return fmt.Errorf("load application domains: %w", err)
	}
	if err := objectStoreRepository.reloadPublicRoutes(ctx); err != nil {
		return fmt.Errorf("load object store domains: %w", err)
	}
	httpServer := &http.Server{
		Addr:              ":443",
		Handler:           ingressRouter,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         tlsConfig,
	}

	rawListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}
	listener := tls.NewListener(netutil.LimitListener(rawListener, maximumHTTPSConnections), tlsConfig)
	defer func() { _ = sdnotify.Stopping("platformd is stopping") }()
	return serveListener(ctx, httpServer, listener, func() error {
		return sdnotify.Ready("platformd admin control plane is ready")
	})
}

func startRegistryUploadCleanup(ctx context.Context, application *registry.Application) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := application.CleanupExpiredUploads(ctx); err != nil && ctx.Err() == nil {
					log.Printf("registry upload cleanup failed: %v", err)
				}
			}
		}
	}()
}

func status(recoveryMode bool) string {
	if recoveryMode {
		return "recovery"
	}
	return "ready"
}

func serve(ctx context.Context, httpServer *http.Server) error {
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return err
	}
	return serveListener(ctx, httpServer, listener, nil)
}

func serveListener(ctx context.Context, httpServer *http.Server, listener net.Listener, started func() error) error {
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- httpServer.Serve(listener)
	}()
	if started != nil {
		if err := started(); err != nil {
			_ = listener.Close()
			<-errChannel
			return err
		}
	}

	select {
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", httpServer.Addr, err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
