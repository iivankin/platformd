package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/sdnotify"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
)

const shutdownTimeout = 120 * time.Second

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
	if err := runtime.ConfigureDeployments(ctx, store, imageCredentials); err != nil {
		return fmt.Errorf("configure service deployments: %w", err)
	}
	if err := runtime.ConfigureServiceWatcher(ctx, store, ""); err != nil {
		return fmt.Errorf("configure service image watcher: %w", err)
	}
	certificates, err := origin.Load(key, installation.OriginCertificates)
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
	tlsConfig := certificates.TLSConfig()
	adminHandler := access.ProtectAdmin(
		installation.AdminHostname,
		verifier,
		server.Handler(
			server.DefaultMeta(status(installation.RecoveryMode)),
			server.WithProjects(liveProjectRepository{store: store, runtime: runtime}),
			server.WithServices(liveServiceRepository{store: store, runtime: runtime}),
			server.WithImageCredentials(imageCredentials),
		),
	)
	httpServer := &http.Server{
		Addr:              ":443",
		Handler:           adminHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         tlsConfig,
	}

	listener, err := tls.Listen("tcp", httpServer.Addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}
	defer func() { _ = sdnotify.Stopping("platformd is stopping") }()
	return serveListener(ctx, httpServer, listener, func() error {
		return sdnotify.Ready("platformd admin control plane is ready")
	})
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
