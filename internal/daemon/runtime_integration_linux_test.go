//go:build linux && integration

package daemon

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/projectnetwork"
	"github.com/iivankin/platformd/internal/state"
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
	projects := []state.RuntimeProject{
		{ID: "integration-a", Name: "alpha", ObjectStoreEnabled: true},
		{ID: "integration-b", Name: "beta"},
	}
	first, err := startRuntime(ctx, paths, tree.WorkloadRoot(), projects)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.networks) != len(projects) || len(first.projectFailures) != 0 {
		_ = first.Close()
		t.Fatalf("unexpected project network reconcile: networks=%v failures=%v", first.networks, first.projectFailures)
	}
	image, err := first.engine.Pull(ctx, containerengine.PullRequest{Reference: "docker.io/library/alpine:3.22"})
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	assertProjectDNS(t, ctx, first, tree, paths, image.ID, "integration-a")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GeneratedRoot, "stale-secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := startRuntime(ctx, paths, tree.WorkloadRoot(), projects)
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

func assertProjectDNS(t *testing.T, ctx context.Context, runtime *runtimeStack, tree *cgrouptree.Tree, paths layout.Paths, imageID, projectID string) {
	t.Helper()
	network := runtime.projectNetworks[projectID]
	parent, err := tree.Parent("daemon-dns")
	if err != nil {
		t.Fatal(err)
	}
	container, err := runtime.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-daemon-dns",
		Command: []string{"/bin/sh", "-c", "sleep 300"},
		Network: network.Name, DNSServers: []string{network.Gateway},
		LogPath:      filepath.Join(paths.LogsRoot, "daemon-dns.log"),
		LogSizeBytes: 1024, LogMaxFiles: 2, CgroupParent: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.engine.RemoveContainer(context.Background(), container.ID, true) })
	if err := runtime.engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatal(err)
	}
	container, err = runtime.engine.InspectContainer(container.ID)
	if err != nil || len(container.IPs[network.Name]) != 1 {
		t.Fatalf("inspect DNS test container: %+v, %v", container, err)
	}
	address, err := netip.ParseAddr(container.IPs[network.Name][0])
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.dnsZones[projectID].Replace(map[string]netip.Addr{"api.alpha.internal": address}); err != nil {
		t.Fatal(err)
	}
	if err := projectnetwork.MarkBridge(projectID); err != nil {
		t.Fatal(err)
	}
	assertLookup := func(expectedCode int, name, expected string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code, execErr := runtime.engine.ExecContainer(ctx, container.ID, containerengine.ExecRequest{
			Command: []string{"nslookup", name}, Stdout: &stdout, Stderr: &stderr,
		})
		if execErr != nil || code != expectedCode || (expected != "" && !bytes.Contains(stdout.Bytes(), []byte(expected))) {
			t.Fatalf("nslookup %s: code=%d stdout=%q stderr=%q err=%v", name, code, stdout.String(), stderr.String(), execErr)
		}
	}
	assertLookup(0, "api.alpha.internal", address.String())
	assertLookup(1, "api.beta.internal", "")
	assertLookup(0, "example.com", "")
	if err := runtime.engine.RemoveContainer(ctx, container.ID, true); err != nil {
		t.Fatal(err)
	}
}
