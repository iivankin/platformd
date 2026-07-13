package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cgroupstats"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerconsole"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/diskpressure"
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
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/sdnotify"
	"github.com/iivankin/platformd/internal/selfupdate"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/terminalauth"
	"github.com/iivankin/platformd/internal/version"
	"github.com/iivankin/platformd/internal/volume"
	"github.com/iivankin/platformd/internal/volumestore"
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
	daemonStartedAt := time.Now()
	ctx, cancelDaemon := context.WithCancel(ctx)
	defer cancelDaemon()
	var updateCommitted atomic.Bool
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
	if err := prepareRuntimeHost(ctx, paths, cgroups.WorkloadRoot()); err != nil {
		return fmt.Errorf("clean runtime before state migration: %w", err)
	}
	resourceUsage, err := cgroupstats.NewProduction(cgroups.WorkloadRoot())
	if err != nil {
		return fmt.Errorf("configure resource usage reader: %w", err)
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
	volumeReferences, err := store.PersistentVolumeReferences(ctx)
	if err != nil {
		return fmt.Errorf("load persistent volume references: %w", err)
	}
	volumeCleanup, err := volumestore.Reconcile(ctx, paths.VolumesRoot, volumeReferences)
	if err != nil {
		return fmt.Errorf("reconcile persistent volumes: %w", err)
	}
	if volumeCleanup.Created != 0 || volumeCleanup.Removed != 0 {
		log.Printf(
			"persistent volume startup reconcile: created=%d removed=%d",
			volumeCleanup.Created, volumeCleanup.Removed,
		)
	}
	auditCleanupContext, cancelAuditCleanup := context.WithCancel(ctx)
	defer cancelAuditCleanup()
	startAuditCleanup(auditCleanupContext, store)
	installation, err := store.Installation(ctx)
	if err != nil {
		return err
	}
	reserve, err := diskpressure.NewFileReserve(0)
	if err != nil {
		return err
	}
	pressure, err := diskpressure.New(diskpressure.Config{
		DataRoot: paths.DataRoot, ReservePath: paths.ReserveFile,
		Collector: diskpressure.StatfsCollector{}, Reserve: reserve, Freezer: cgroups,
		Transitions: diskPressureAuditSink{store: store, installationID: installation.ID},
	})
	if err != nil {
		return err
	}
	if _, err := pressure.Check(ctx); err != nil {
		return fmt.Errorf("initialize disk pressure: %w", err)
	}
	pressureContext, cancelPressure := context.WithCancel(ctx)
	pressureDone := make(chan struct{})
	defer func() {
		cancelPressure()
		<-pressureDone
	}()
	go func() {
		defer close(pressureDone)
		err := pressure.Run(pressureContext, func(checkErr error) { log.Printf("disk pressure check: %v", checkErr) })
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("disk pressure monitor stopped: %v", err)
		}
	}()
	projects, err := store.RuntimeProjects(ctx)
	if err != nil {
		return err
	}
	mutationAdmission := admission.New()
	runtime, err := startRuntime(ctx, paths, cgroups.WorkloadRoot(), projects, pressure, mutationAdmission)
	if err != nil {
		return err
	}
	defer func() {
		if updateCommitted.Load() {
			returnErr = errors.Join(returnErr, runtime.ReleaseForUpdate())
			return
		}
		returnErr = errors.Join(returnErr, runtime.Close())
	}()
	logCleaner, err := containerlogs.NewCleaner(containerlogs.CleanerConfig{
		Root: paths.LogsRoot, Retention: containerLogRetention,
	})
	if err != nil {
		return err
	}
	logCleanupContext, cancelLogCleanup := context.WithCancel(ctx)
	logCleanupDone := make(chan struct{})
	defer func() {
		cancelLogCleanup()
		<-logCleanupDone
	}()
	go func() {
		defer close(logCleanupDone)
		runContainerLogCleanup(logCleanupContext, logCleaner, runtime.engine.ActiveLogPaths)
	}()
	releasePublicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return err
	}
	platformUpdater, err := selfupdate.New(selfupdate.Config{
		Paths: paths, ExpectedUID: 0, ManifestURL: releaseconfig.LatestManifestURL,
		PublicKey: releasePublicKey, Admission: mutationAdmission, Growth: pressure,
		QuiesceWorkloads: func(updateContext context.Context) (selfupdate.ResumeWorkloads, error) {
			bounded, cancel := context.WithTimeout(updateContext, 90*time.Second)
			defer cancel()
			return runtime.QuiesceWorkloads(bounded)
		},
	})
	if err != nil {
		return fmt.Errorf("configure self-update: %w", err)
	}
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
	startRegistryUploadCleanup(ctx, registryApplication, mutationAdmission)
	backupTargetGate := backup.NewGate()
	backupTargets, err := backup.NewTargetApplication(store, key, backupTargetGate, nil, nil, nil)
	if err != nil {
		return err
	}
	var dirtyControl *backup.DirtyTracker
	var controlJob *backup.ControlJob
	if !installation.RecoveryMode {
		dirtyControl = backup.NewDirtyTracker()
		store.SetControlCommitObserver(func() { dirtyControl.Mark(time.Now()) })
		defer store.SetControlCommitObserver(nil)
		controlJob, err = backup.NewControlJob(backup.ControlJobConfig{
			Store: store, Target: backupTargets, TargetGate: backupTargetGate,
			Admission: mutationAdmission, Growth: pressure, Master: key,
			InstallationID: installation.ID, WorkRoot: paths.BackupWorkRoot, ExpectedUID: 0,
			PublicKey:   releasePublicKey,
			ReleaseSlot: func() (string, error) { return filepath.EvalSymlinks(paths.Current) },
		})
		if err != nil {
			return fmt.Errorf("configure control backup job: %w", err)
		}
	}
	if err := runtime.ConfigureManagedPostgres(store, key); err != nil {
		return fmt.Errorf("configure managed PostgreSQL: %w", err)
	}
	if err := runtime.ConfigureManagedRedis(store, key); err != nil {
		return fmt.Errorf("configure managed Redis: %w", err)
	}
	if err := runtime.ConfigureDeployments(ctx, store, imageCredentials, registryApplication); err != nil {
		return fmt.Errorf("configure service deployments: %w", err)
	}
	if !installation.RecoveryMode {
		if err := runtime.ReconcileManagedPostgres(ctx, store); err != nil {
			return fmt.Errorf("reconcile managed PostgreSQL: %w", err)
		}
		if err := runtime.ReconcileManagedRedis(ctx, store); err != nil {
			return fmt.Errorf("reconcile managed Redis: %w", err)
		}
		if err := runtime.ReconcileDeployments(ctx, store); err != nil {
			return fmt.Errorf("reconcile service deployments: %w", err)
		}
	}
	containerConsole, err := containerconsole.New(containerconsole.Config{
		Services: store, Runtime: runtime.deployments, Audit: store,
	})
	if err != nil {
		return fmt.Errorf("configure container console: %w", err)
	}
	volumeApplication, err := volume.New(volume.Config{
		Repository: store, Filesystem: volume.NewLocalFilesystem(paths.VolumesRoot), Images: runtime.engine,
		OnCleanupError: func(cleanupErr error) { log.Printf("volume cleanup: %v", cleanupErr) },
	})
	if err != nil {
		return fmt.Errorf("configure ordinary volumes: %w", err)
	}
	if !installation.RecoveryMode {
		if err := runtime.ConfigureServiceWatcher(ctx, store, registryHostname); err != nil {
			return fmt.Errorf("configure service image watcher: %w", err)
		}
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
	var disasterRecoveryProgress *recoveryProgress
	if installation.RecoveryMode {
		disasterRecoveryProgress = newRecoveryProgress()
	}
	restoreService, err := backup.NewResourceRestoreService(backup.ResourceRestoreServiceConfig{
		Context: ctx, Store: store, Target: backupTargets, TargetGate: backupTargetGate,
		Admission: mutationAdmission, Master: key,
		Restorers: resourceRestorers(runtime, registryApplication, objectStoreApplication),
		OnError:   func(restoreErr error) { log.Printf("resource restore: %v", restoreErr) },
		OnSuccess: func(request backup.ResourceRestoreRequest) {
			if disasterRecoveryProgress != nil {
				disasterRecoveryProgress.markManual(request)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("configure resource restore jobs: %w", err)
	}
	var backupResources *backup.ResourceApplication
	if !installation.RecoveryMode {
		resourceJob, err := backup.NewResourceJob(backup.ResourceJobConfig{
			Store: store, Target: backupTargets, TargetGate: backupTargetGate,
			Admission: mutationAdmission, Growth: pressure, Master: key, WorkRoot: paths.BackupWorkRoot,
			Exporters: map[string]backup.ResourceExporter{
				"postgres": backup.ResourceExporterFunc(func(exportContext context.Context, resourceID string) (backup.ResourceExport, error) {
					reader, err := runtime.OpenManagedPostgresBackup(exportContext, resourceID)
					return backup.ResourceExport{Reader: reader}, err
				}),
				"redis": backup.ResourceExporterFunc(func(exportContext context.Context, resourceID string) (backup.ResourceExport, error) {
					reader, err := runtime.OpenManagedRedisBackup(exportContext, resourceID)
					return backup.ResourceExport{Reader: reader}, err
				}),
				"registry": backup.ResourceExporterFunc(func(exportContext context.Context, resourceID string) (backup.ResourceExport, error) {
					export, err := registryApplication.BackupSnapshot(exportContext, resourceID)
					return backup.ResourceExport{Reader: export.Reader, Release: export.Release}, err
				}),
				"object_store": backup.ResourceExporterFunc(func(exportContext context.Context, resourceID string) (backup.ResourceExport, error) {
					export, err := objectStoreApplication.BackupSnapshot(exportContext, resourceID)
					return backup.ResourceExport{
						Reader:          io.NopCloser(bytes.NewReader(export.Metadata)),
						AttachmentPaths: export.AttachmentPaths, Release: export.Release,
					}, err
				}),
			},
		})
		if err != nil {
			return fmt.Errorf("configure resource backup jobs: %w", err)
		}
		backupWorker, err := backup.NewScheduledWorker(backup.WorkerConfig{
			Dirty: dirtyControl, Control: controlJob, Store: store, Resources: resourceJob,
			StartedAt: daemonStartedAt,
			OnError:   func(workerErr error) { log.Printf("backup worker: %v", workerErr) },
		})
		if err != nil {
			return err
		}
		backupResources, err = backup.NewResourceApplication(backup.ResourceApplicationConfig{
			Store: store, Worker: backupWorker, Target: backupTargets,
			TargetGate: backupTargetGate, Master: key, Restores: restoreService,
		})
		if err != nil {
			return fmt.Errorf("configure resource backup application: %w", err)
		}
		workerContext, cancelWorker := context.WithCancel(ctx)
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			if err := backupWorker.Run(workerContext); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("backup worker stopped: %v", err)
			}
		}()
		defer func() {
			cancelWorker()
			<-workerDone
		}()
		if _, configured, targetErr := backupTargets.Target(ctx); targetErr != nil {
			return targetErr
		} else if configured {
			dirtyControl.Mark(time.Now())
		}
	} else {
		backupResources, err = backup.NewResourceApplication(backup.ResourceApplicationConfig{
			Store: store, Target: backupTargets, TargetGate: backupTargetGate,
			Master: key, Restores: restoreService,
		})
		if err != nil {
			return fmt.Errorf("configure recovery backup application: %w", err)
		}
	}
	objectStoreHandler, err := objectstore.NewHTTPHandler(objectstore.HTTPConfig{
		Application: objectStoreApplication,
		Admission:   mutationAdmission,
		LookupHost:  store.ObjectStoreByHostname,
	})
	if err != nil {
		return err
	}
	if !installation.RecoveryMode {
		if err := runtime.ConfigureObjectStores(ctx, store, objectStoreHandler); err != nil {
			return fmt.Errorf("configure managed S3: %w", err)
		}
	}
	registryHandler, err := registry.NewHTTPHandler(registryApplication, automationauth.NewInMemoryFailureLimiter(), mutationAdmission)
	if err != nil {
		return err
	}
	publicRegistryHandler, err := newAvailabilityHandler(registryHandler, !installation.RecoveryMode)
	if err != nil {
		return err
	}
	publicObjectStoreHandler, err := newAvailabilityHandler(objectStoreHandler, !installation.RecoveryMode)
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
	databaseVersions, err := databaseversion.New(databaseversion.Config{
		Context: ctx, Store: store, Admission: mutationAdmission,
		Adapters: map[string]databaseversion.Adapter{
			databaseversion.Postgres: postgresVersionAdapter{store: store, runtime: runtime},
			databaseversion.Redis:    redisVersionAdapter{store: store, runtime: runtime},
		},
		OnError: func(err error) { log.Printf("managed database version change: %v", err) },
	})
	if err != nil {
		return err
	}
	serverTerminalAuth, err := terminalauth.New(terminalauth.Config{
		Master: key, InstallationID: installation.ID, Verifier: installation.ConsolePassphrasePHC,
	})
	if err != nil {
		return fmt.Errorf("configure server terminal authentication: %w", err)
	}
	registrySettings := &liveRegistrySettings{store: store, runtime: runtime, certificates: certificates}
	var automationHostname string
	var automationHandler http.Handler
	if installation.AutomationHostname != nil {
		automationHostname = *installation.AutomationHostname
		automationRepository := liveAutomationRepository{store: store, runtime: runtime}
		projectAutomation, err := automation.NewProjectApplication(automationRepository, nil, nil)
		if err != nil {
			return err
		}
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
		managedResourceAutomation, err := automation.NewManagedResourceApplication(automationRepository)
		if err != nil {
			return err
		}
		volumeAutomation, err := automation.NewVolumeApplication(volumeApplication)
		if err != nil {
			return err
		}
		logAutomation, err := automation.NewLogApplication(store, logReader)
		if err != nil {
			return err
		}
		automationAPI, err := automationapi.Handler(automationapi.Config{
			Hostname: automationHostname, Repository: automationRepository, Projects: projectAutomation,
			Services: serviceAutomation,
			Logs:     logAutomation, Images: managedImageCatalog, Redis: redisAutomation,
			RedisStore: automationRepository, Postgres: postgresAutomation,
			PostgresStore: automationRepository, ObjectStores: objectStoreApplication,
			Managed:   managedResourceAutomation,
			Versions:  databaseVersions,
			Volumes:   volumeAutomation,
			Admission: mutationAdmission,
		})
		if err != nil {
			return err
		}
		mcpHandler, err := mcp.New(mcp.Config{
			Hostname: automationHostname, Version: version.Version, Repository: automationRepository,
			Services: serviceAutomation, Logs: logAutomation, Images: managedImageCatalog,
			Redis: redisAutomation, Postgres: postgresAutomation, Managed: managedResourceAutomation,
			Versions: databaseVersions, Volumes: volumeAutomation,
			Admission: mutationAdmission,
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
	var recoveryRepository server.RecoveryRepository
	if disasterRecoveryProgress != nil {
		recoveryRepository = &liveRecoveryRepository{store: store, progress: disasterRecoveryProgress}
	}
	adminApplicationHandler := server.Handler(
		server.DefaultMeta(status(installation.RecoveryMode)),
		server.WithProjects(liveProjectRepository{store: store, runtime: runtime}),
		server.WithServices(liveServiceRepository{store: store, runtime: runtime}),
		server.WithVolumes(volumeApplication),
		server.WithImageCredentials(imageCredentials),
		server.WithDomains(domains),
		server.WithAPITokens(apiTokens),
		server.WithLogs(installation.AdminHostname, logs),
		server.WithAudit(store),
		server.WithManagedImages(managedImageCatalog),
		server.WithManagedRedis(managedRedisApplication),
		server.WithManagedPostgres(managedPostgresApplication),
		server.WithObjectStores(objectStoreApplication),
		server.WithRegistry(registryApplication, registrySettings),
		server.WithBackupTargets(backupTargets),
		server.WithBackupResources(backupResources),
		server.WithDatabaseVersions(databaseVersions),
		server.WithContainerConsole(installation.AdminHostname, containerConsole),
		server.WithServerTerminalAuth(serverTerminalAuth),
		server.WithDiskPressure(pressure),
		server.WithResourceUsage(resourceUsage),
		server.WithAdmission(mutationAdmission),
		server.WithSelfUpdate(platformUpdater, func() {
			updateCommitted.Store(true)
			cancelDaemon()
		}),
		server.WithRecovery(recoveryRepository),
	)
	if installation.RecoveryMode {
		adminApplicationHandler = recoveryAdminHandler{target: adminApplicationHandler}
	}
	adminHandler := access.ProtectAdmin(installation.AdminHostname, verifier, adminApplicationHandler)
	if installation.RecoveryMode && automationHandler != nil {
		automationHandler, err = newAvailabilityHandler(automationHandler, false)
		if err != nil {
			return err
		}
	}
	ingressRouter, err := ingress.New(ingress.Config{
		AdminHostname: installation.AdminHostname, AdminHandler: adminHandler,
		AutomationHostname: automationHostname, AutomationHandler: automationHandler,
		RegistryHostname: registryHostname, RegistryHandler: publicRegistryHandler,
		ObjectStoreHandler: publicObjectStoreHandler,
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
	if !installation.RecoveryMode {
		if err := objectStoreRepository.reloadPublicRoutes(ctx); err != nil {
			return fmt.Errorf("load object store domains: %w", err)
		}
	}
	var disasterRecovery recoveryAttempt
	if installation.RecoveryMode {
		disasterRecovery, err = newRecoveryAttempt(recoveryConfig{
			Store: store, Target: backupTargets, TargetGate: backupTargetGate,
			Admission: mutationAdmission, Master: key,
			Installation: installation, Runtime: runtime, Registry: registryApplication,
			ObjectStore: objectStoreApplication, Progress: disasterRecoveryProgress,
		})
		if err != nil {
			return fmt.Errorf("configure automatic disaster recovery: %w", err)
		}
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
		if err := sdnotify.Ready("platformd admin control plane is ready"); err != nil {
			return err
		}
		if err := bootstrap.FinalizeSuccessfulUpdate(paths, releasePublicKey, 0); err != nil {
			log.Printf("release readiness cleanup failed: %v", err)
		}
		if installation.RecoveryMode {
			startRecoveryLoop(ctx, disasterRecovery, disasterRecoveryProgress, cancelDaemon)
		}
		return nil
	})
}

func startRegistryUploadCleanup(ctx context.Context, application *registry.Application, gate *admission.Gate) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				lease, admitErr := gate.Begin("registry_cleanup", "expired_uploads")
				if admitErr != nil {
					continue
				}
				if _, err := application.CleanupExpiredUploads(ctx); err != nil && ctx.Err() == nil {
					log.Printf("registry upload cleanup failed: %v", err)
				}
				lease.Release()
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
