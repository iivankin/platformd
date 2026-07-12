//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/containers/podman/v5/libpod"
	commonconfig "go.podman.io/common/pkg/config"
	"go.podman.io/storage"
)

var runtimeSingleton struct {
	sync.Mutex
	open bool
}

type Engine struct {
	runtime *libpod.Runtime
	config  Config

	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, config Config) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := validateRuntimeFiles(config); err != nil {
		return nil, err
	}

	runtimeSingleton.Lock()
	if runtimeSingleton.open {
		runtimeSingleton.Unlock()
		return nil, fmt.Errorf("container runtime is already open")
	}
	runtimeSingleton.open = true
	runtimeSingleton.Unlock()

	opened := false
	defer func() {
		if !opened {
			runtimeSingleton.Lock()
			runtimeSingleton.open = false
			runtimeSingleton.Unlock()
		}
	}()

	for _, directory := range []string{
		config.RunRoot,
		config.GraphRoot,
		config.LogRoot,
		config.StaticDir,
		config.VolumePath,
		config.NetworkConfigDir,
		config.HooksDir,
		config.CDISpecDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create runtime directory %s: %w", directory, err)
		}
	}

	// containers/common consults these variables before RuntimeOptions are
	// applied. Platformd is a singleton and establishes the private boundary
	// once, before any concurrent runtime work begins.
	if err := os.Setenv("CONTAINERS_CONF", config.ContainersConf); err != nil {
		return nil, err
	}
	if err := os.Setenv("CONTAINERS_STORAGE_CONF", config.StorageConf); err != nil {
		return nil, err
	}

	runtime, err := libpod.NewRuntime(ctx,
		libpod.WithStorageConfig(storage.StoreOptions{
			RunRoot:         config.RunRoot,
			GraphRoot:       config.GraphRoot,
			GraphDriverName: "overlay",
		}),
		libpod.WithOCIRuntime(config.OCIRuntime),
		libpod.WithConmonPath(config.Conmon),
		libpod.WithNetworkBackend("netavark"),
		libpod.WithCgroupManager(commonconfig.CgroupfsCgroupsManager),
		libpod.WithStaticDir(config.StaticDir),
		libpod.WithTmpDir(config.StaticDir),
		libpod.WithNetworkConfigDir(config.NetworkConfigDir),
		libpod.WithVolumePath(config.VolumePath),
		libpod.WithRegistriesConf(config.RegistriesConf),
		libpod.WithDatabaseBackend("sqlite"),
		libpod.WithHooksDir(config.HooksDir),
		libpod.WithCDISpecDirs([]string{config.CDISpecDir}),
		libpod.WithDefaultMountsFile(config.DefaultMountsFile),
		libpod.WithEventsLogger("file"),
		libpod.WithSignalHandling(false),
		libpod.WithConmonCleanupCommand(false),
	)
	if err != nil {
		return nil, fmt.Errorf("open private libpod runtime: %w", err)
	}

	opened = true
	return &Engine{runtime: runtime, config: config}, nil
}

func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		e.closeErr = e.runtime.Shutdown(false)
		runtimeSingleton.Lock()
		runtimeSingleton.open = false
		runtimeSingleton.Unlock()
	})
	return e.closeErr
}

func validateRuntimeFiles(config Config) error {
	for _, path := range []string{
		config.ContainersConf,
		config.StorageConf,
		config.RegistriesConf,
		config.SignaturePolicy,
		config.SeccompProfile,
		config.DefaultMountsFile,
	} {
		if err := requireRegularFile(path, false); err != nil {
			return fmt.Errorf("validate runtime file: %w", err)
		}
	}
	for _, path := range []string{config.OCIRuntime, config.Conmon} {
		if err := requireRegularFile(path, true); err != nil {
			return fmt.Errorf("validate runtime executable: %w", err)
		}
	}
	return nil
}
