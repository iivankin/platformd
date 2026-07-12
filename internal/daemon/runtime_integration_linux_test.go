//go:build linux && integration

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/layout"
)

func TestMain(m *testing.M) {
	if containerengine.InitReexec() {
		return
	}
	os.Exit(m.Run())
}

func TestRuntimeStartupRecreatesTransientStateAndKeepsImageCache(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" || os.Getenv("PLATFORMD_CGROUP_INTEGRATION") != "1" {
		t.Skip("set runtime and cgroup integration flags inside an isolated delegated systemd unit")
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	paths := layout.FromRoots(
		"/var/lib/platformd-daemon-integration",
		"/etc/platformd-daemon-integration",
		"/run/platformd-daemon-integration",
		"/tmp/platformd-daemon-integration",
		"/tmp/platformd-daemon-integration.service",
	)
	for _, root := range []string{paths.DataRoot, paths.ConfigRoot, paths.RuntimeRoot} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	if err := os.MkdirAll(paths.ReleasesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/var/lib/platformd/releases/integration", paths.Current); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	first, err := startRuntime(ctx, paths, tree.WorkloadRoot())
	if err != nil {
		t.Fatal(err)
	}
	image, err := first.engine.Pull(ctx, containerengine.PullRequest{Reference: "docker.io/library/alpine:3.22"})
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GeneratedRoot, "stale-secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := startRuntime(ctx, paths, tree.WorkloadRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.engine.InspectImage(ctx, image.ID); err != nil {
		t.Fatalf("inspect image preserved across startup: %v", err)
	}
	entries, err := os.ReadDir(paths.GeneratedRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("generated runtime state survived restart: %v, %v", entries, err)
	}
}
