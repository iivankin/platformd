//go:build linux && integration

package containerengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
)

func TestDelegatedCgroupPlacementAndLimits(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" || os.Getenv("PLATFORMD_CGROUP_INTEGRATION") != "1" {
		t.Skip("set runtime and cgroup integration flags inside an isolated delegated systemd unit")
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeIntegrationConfig()
	config.CgroupWorkloadRoot = tree.WorkloadRoot()
	for _, directory := range []string{config.LogRoot, config.AllowedMountRoots[0], config.AllowedMountRoots[1]} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	image, err := engine.Pull(ctx, PullRequest{Reference: integrationAlpineImage})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := tree.Parent("integration-runtime")
	if err != nil {
		t.Fatal(err)
	}

	unlimited := createCgroupTestContainer(t, ctx, engine, image.ID, parent, "unlimited", 0, 0)
	assertCgroupPlacement(t, unlimited, parent)
	assertCgroupFile(t, unlimited.Pid, "memory.max", "max")
	if value := readCgroupFile(t, unlimited.Pid, "cpu.max"); !strings.HasPrefix(value, "max ") {
		t.Fatalf("unlimited cpu.max = %q", value)
	}
	removeCgroupTestContainer(t, ctx, engine, unlimited.ID)

	const memoryLimit = int64(64 << 20)
	limited := createCgroupTestContainer(t, ctx, engine, image.ID, parent, "limited", 500, memoryLimit)
	assertCgroupPlacement(t, limited, parent)
	assertCgroupFile(t, limited.Pid, "memory.max", fmt.Sprint(memoryLimit))
	assertCgroupFile(t, limited.Pid, "cpu.max", "50000 100000")
	removeCgroupTestContainer(t, ctx, engine, limited.ID)
}

func createCgroupTestContainer(t *testing.T, ctx context.Context, engine *Engine, imageID, parent, name string, cpu, memory int64) Container {
	t.Helper()
	container, err := engine.CreateContainer(ctx, ContainerSpec{
		ImageID: imageID, Name: "platformd-cgroup-" + name,
		Command:      []string{"/bin/sh", "-c", "sleep 300"},
		LogPath:      filepath.Join(runtimeIntegrationConfig().LogRoot, "cgroup-"+name+".log"),
		LogSizeBytes: 1024, LogMaxFiles: 2,
		CgroupParent: parent, CPUMillicores: cpu, MemoryMaxBytes: memory,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.RemoveContainer(context.Background(), container.ID, true) })
	if err := engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatal(err)
	}
	container, err = engine.InspectContainer(container.ID)
	if err != nil || container.Pid <= 0 || container.ConmonPID <= 0 {
		t.Fatalf("inspect cgroup test container: %+v, %v", container, err)
	}
	return container
}

func removeCgroupTestContainer(t *testing.T, ctx context.Context, engine *Engine, containerID string) {
	t.Helper()
	if err := engine.RemoveContainer(ctx, containerID, true); err != nil {
		t.Fatal(err)
	}
}

func assertCgroupPlacement(t *testing.T, container Container, parent string) {
	t.Helper()
	payloadPath := processCgroup(t, container.Pid)
	if !strings.HasPrefix(payloadPath, parent+"/libpod-") {
		t.Fatalf("payload cgroup %q escaped parent %q", payloadPath, parent)
	}
	if conmonPath, ownPath := processCgroup(t, container.ConmonPID), processCgroup(t, os.Getpid()); conmonPath != ownPath || filepath.Base(conmonPath) != "control" {
		t.Fatalf("conmon cgroup = %q, platformd cgroup = %q", conmonPath, ownPath)
	}
}

func assertCgroupFile(t *testing.T, pid int, name, expected string) {
	t.Helper()
	if value := readCgroupFile(t, pid, name); value != expected {
		t.Fatalf("%s = %q, want %q", name, value, expected)
	}
}

func readCgroupFile(t *testing.T, pid int, name string) string {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(processCgroup(t, pid), "/"), name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(value))
}

func processCgroup(t *testing.T, pid int) string {
	t.Helper()
	value, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(value)) {
		if strings.HasPrefix(line, "0::/") {
			return strings.TrimSpace(strings.TrimPrefix(line, "0::"))
		}
	}
	t.Fatalf("PID %d has no unified cgroup: %q", pid, value)
	return ""
}
