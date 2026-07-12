//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moby/sys/mountinfo"
	"go.podman.io/storage"
	"golang.org/x/sys/unix"
)

type StorageCleanupResult struct {
	RemovedContainers int
	PreservedImages   int
	CacheReset        bool
	ResetReason       string
}

// PrepareStorage removes every container record and writable layer from the
// private graphroot before libpod is opened. Images are retained as cache. A
// graphroot that cannot be opened is disposable and is recreated empty.
func PrepareStorage(ctx context.Context, config Config) (StorageCleanupResult, error) {
	if err := config.Validate(); err != nil {
		return StorageCleanupResult{}, err
	}
	if err := requireRegularFile(config.StorageConf, false); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("validate storage config: %w", err)
	}
	if err := os.Setenv("CONTAINERS_STORAGE_CONF", config.StorageConf); err != nil {
		return StorageCleanupResult{}, err
	}
	// Libpod databases and locks are deliberately ephemeral product state. The
	// graphroot is outside this tree so image layers remain available as cache.
	if err := os.RemoveAll(config.TransientRoot); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("clear transient runtime state: %w", err)
	}
	if err := os.MkdirAll(config.TransientRoot, 0o700); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("create transient runtime root: %w", err)
	}

	cleanupRunRoot := config.RunRoot + "-cleanup"
	if err := os.RemoveAll(cleanupRunRoot); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("clear cleanup runroot: %w", err)
	}
	if err := os.MkdirAll(cleanupRunRoot, 0o700); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("create cleanup runroot: %w", err)
	}
	defer os.RemoveAll(cleanupRunRoot)

	store, err := storage.GetStore(storage.StoreOptions{
		RunRoot:         cleanupRunRoot,
		GraphRoot:       config.GraphRoot,
		GraphDriverName: "overlay",
	})
	if err != nil {
		if resetErr := resetGraphRoot(config.GraphRoot); resetErr != nil {
			return StorageCleanupResult{}, fmt.Errorf("open graphroot: %v; reset graphroot: %w", err, resetErr)
		}
		return StorageCleanupResult{CacheReset: true, ResetReason: err.Error()}, nil
	}
	defer store.Free()

	images, err := store.Images()
	if err != nil {
		_, _ = store.Shutdown(true)
		return StorageCleanupResult{}, fmt.Errorf("list cached images: %w", err)
	}
	containers, err := store.Containers()
	if err != nil {
		_, _ = store.Shutdown(true)
		return StorageCleanupResult{}, fmt.Errorf("list storage containers: %w", err)
	}
	result := StorageCleanupResult{PreservedImages: len(images)}
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			_, _ = store.Shutdown(true)
			return StorageCleanupResult{}, err
		}
		if _, err := store.Unmount(container.ID, true); err != nil {
			_, _ = store.Shutdown(true)
			return StorageCleanupResult{}, fmt.Errorf("unmount storage container %s: %w", container.ID, err)
		}
		if err := store.DeleteContainer(container.ID); err != nil {
			_, _ = store.Shutdown(true)
			return StorageCleanupResult{}, fmt.Errorf("delete storage container %s: %w", container.ID, err)
		}
		result.RemovedContainers++
	}

	remaining, err := store.Containers()
	if err != nil {
		_, _ = store.Shutdown(true)
		return StorageCleanupResult{}, fmt.Errorf("verify empty container store: %w", err)
	}
	if len(remaining) != 0 {
		_, _ = store.Shutdown(true)
		return StorageCleanupResult{}, fmt.Errorf("container store still has %d records", len(remaining))
	}
	if _, err := store.Shutdown(false); err != nil {
		return StorageCleanupResult{}, fmt.Errorf("shutdown cleanup store: %w", err)
	}
	return result, nil
}

func resetGraphRoot(graphRoot string) error {
	mounts, err := mountinfo.GetMounts(mountinfo.PrefixFilter(graphRoot))
	if err != nil {
		return fmt.Errorf("list graphroot mounts: %w", err)
	}
	sort.Slice(mounts, func(i, j int) bool {
		leftDepth := strings.Count(filepath.Clean(mounts[i].Mountpoint), string(filepath.Separator))
		rightDepth := strings.Count(filepath.Clean(mounts[j].Mountpoint), string(filepath.Separator))
		return leftDepth > rightDepth
	})
	for _, mount := range mounts {
		if err := unix.Unmount(mount.Mountpoint, unix.MNT_DETACH); err != nil {
			return fmt.Errorf("unmount %s: %w", mount.Mountpoint, err)
		}
	}
	if err := os.RemoveAll(graphRoot); err != nil {
		return err
	}
	return os.MkdirAll(graphRoot, 0o700)
}
