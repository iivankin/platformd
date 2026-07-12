//go:build linux && amd64 && cgo && integration

package containerengine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	integrationDataRoot    = "/var/lib/platformd-integration"
	integrationRuntimeRoot = "/run/platformd-integration"
	integrationReleaseRoot = "/var/lib/platformd/releases/current/runtime"
)

func TestMain(m *testing.M) {
	if InitReexec() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestPrivateRuntimeLifecycle(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_RUNTIME_INTEGRATION=1 on an isolated root host")
	}

	config := runtimeIntegrationConfig()
	for _, directory := range []string{config.LogRoot, config.AllowedMountRoots[0], config.AllowedMountRoots[1]} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})

	image, err := engine.Pull(ctx, PullRequest{Reference: "docker.io/library/alpine:3.22", Refresh: true})
	if err != nil {
		t.Fatalf("pull image: %v", err)
	}
	network, err := engine.CreateNetwork(NetworkSpec{
		Name:      "platformd-integration",
		Interface: "pdit0",
		Subnet:    "10.89.43.0/24",
		Gateway:   "10.89.43.1",
		Labels:    map[string]string{"io.platformd.test": "runtime"},
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	t.Cleanup(func() { _ = engine.RemoveNetwork(network.Name) })

	logPath := filepath.Join(config.LogRoot, "runtime.log")
	container, err := engine.CreateContainer(ctx, ContainerSpec{
		ImageID: image.ID,
		Name:    "platformd-integration",
		Command: []string{
			"/bin/sh", "-c",
			`i=0; while [ "$i" -lt 300 ]; do echo "platformd-runtime-rotation-$i-abcdefghijklmnopqrstuvwxyz"; i=$((i+1)); done; sleep 2`,
		},
		Labels:       map[string]string{"io.platformd.test": "runtime"},
		Network:      network.Name,
		DNSServers:   []string{network.Gateway},
		LogPath:      logPath,
		LogSizeBytes: 1024,
		LogMaxFiles:  3,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() { _ = engine.RemoveContainer(context.Background(), container.ID, true) })
	if err := engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatalf("start container: %v", err)
	}

	inspected, err := engine.InspectContainer(container.ID)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	if len(inspected.IPs[network.Name]) != 1 {
		t.Fatalf("unexpected network addresses: %+v", inspected.IPs)
	}

	var stdout bytes.Buffer
	exitCode, err := engine.ExecContainer(ctx, container.ID, ExecRequest{
		Command: []string{"/bin/sh", "-c", "printf runtime-exec-ok"},
		Stdout:  &stdout,
	})
	if err != nil || exitCode != 0 || stdout.String() != "runtime-exec-ok" {
		t.Fatalf("exec mismatch: code=%d stdout=%q err=%v", exitCode, stdout.String(), err)
	}

	cancelCtx, cancelExec := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelExec()
	started := time.Now()
	_, err = engine.ExecContainer(cancelCtx, container.ID, ExecRequest{Command: []string{"sleep", "30"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel exec: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("cancelled exec took too long: %s", time.Since(started))
	}

	code, err := engine.WaitContainer(ctx, container.ID)
	if err != nil || code != 0 {
		t.Fatalf("wait container: code=%d err=%v", code, err)
	}
	if err := engine.RemoveContainer(ctx, container.ID, false); err != nil {
		t.Fatalf("remove container: %v", err)
	}
	if err := engine.RemoveNetwork(network.Name); err != nil {
		t.Fatalf("remove network: %v", err)
	}

	logs, err := filepath.Glob(logPath + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) < 2 || len(logs) > 4 {
		t.Fatalf("expected active plus rotated logs, got %v", logs)
	}
}

func TestPrepareStoragePurgesContainersAndKeepsImages(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_RUNTIME_INTEGRATION=1 on an isolated root host")
	}
	config := runtimeIntegrationConfig()
	for _, directory := range []string{config.LogRoot, config.AllowedMountRoots[0], config.AllowedMountRoots[1]} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	image, err := engine.Pull(ctx, PullRequest{Reference: "docker.io/library/alpine:3.22"})
	if err != nil {
		t.Fatalf("pull image: %v", err)
	}
	container, err := engine.CreateContainer(ctx, ContainerSpec{
		ImageID:      image.ID,
		Name:         "platformd-interrupted",
		Command:      []string{"sleep", "30"},
		Labels:       map[string]string{"io.platformd.test": "interrupted"},
		LogPath:      filepath.Join(config.LogRoot, "interrupted.log"),
		LogSizeBytes: 1024,
		LogMaxFiles:  2,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatalf("start container: %v", err)
	}
	if err := engine.StopContainer(container.ID, 1); err != nil {
		t.Fatalf("stop container: %v", err)
	}
	if code, err := engine.WaitContainer(ctx, container.ID); err != nil {
		t.Fatalf("cleanup stopped container: code=%d err=%v", code, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("close interrupted runtime: %v", err)
	}

	cleanup, err := PrepareStorage(ctx, config)
	if err != nil {
		t.Fatalf("prepare storage: %v", err)
	}
	if cleanup.RemovedContainers != 1 || cleanup.PreservedImages < 1 || cleanup.CacheReset {
		t.Fatalf("unexpected cleanup result: %+v", cleanup)
	}

	reopened, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("reopen runtime: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.InspectImage(ctx, image.ID); err != nil {
		t.Fatalf("cached image was not preserved: %v", err)
	}
	if _, err := reopened.InspectContainer(container.ID); err == nil {
		t.Fatal("stale container survived startup cleanup")
	}
}

func runtimeIntegrationConfig() Config {
	return Config{
		TransientRoot:     integrationRuntimeRoot,
		RunRoot:           filepath.Join(integrationRuntimeRoot, "runroot"),
		GraphRoot:         filepath.Join(integrationDataRoot, "storage"),
		LogRoot:           filepath.Join(integrationDataRoot, "logs"),
		StaticDir:         filepath.Join(integrationRuntimeRoot, "libpod"),
		VolumePath:        filepath.Join(integrationRuntimeRoot, "volumes"),
		NetworkConfigDir:  filepath.Join(integrationRuntimeRoot, "networks"),
		HooksDir:          filepath.Join(integrationRuntimeRoot, "hooks"),
		CDISpecDir:        filepath.Join(integrationRuntimeRoot, "cdi"),
		ContainersConf:    filepath.Join(integrationReleaseRoot, "containers.conf"),
		StorageConf:       filepath.Join(integrationReleaseRoot, "storage.conf"),
		RegistriesConf:    filepath.Join(integrationReleaseRoot, "registries.conf"),
		SignaturePolicy:   filepath.Join(integrationReleaseRoot, "policy.json"),
		SeccompProfile:    filepath.Join(integrationReleaseRoot, "seccomp.json"),
		DefaultMountsFile: filepath.Join(integrationReleaseRoot, "mounts.conf"),
		OCIRuntime:        filepath.Join(integrationReleaseRoot, "crun"),
		Conmon:            filepath.Join(integrationReleaseRoot, "conmon"),
		AllowedMountRoots: []string{filepath.Join(integrationDataRoot, "volumes"), filepath.Join(integrationRuntimeRoot, "generated")},
	}
}
