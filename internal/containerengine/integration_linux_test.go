//go:build linux && amd64 && cgo && integration

package containerengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
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
	if err := os.RemoveAll(config.LogRoot); err != nil {
		t.Fatal(err)
	}
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

	terminalInput, terminalWriter := io.Pipe()
	defer terminalWriter.Close()
	var terminalOutput bytes.Buffer
	resizes := make(chan TerminalSize, 1)
	type terminalResult struct {
		code int
		err  error
	}
	terminalDone := make(chan terminalResult, 1)
	go func() {
		code, execErr := engine.ExecTerminalContainer(ctx, container.ID, TerminalExecRequest{
			Command: []string{"/bin/sh", "-c", `stty size; IFS= read -r line; stty size; printf '<%s>' "$line"`},
			Stdin:   terminalInput, Output: &terminalOutput,
			InitialSize: TerminalSize{Cols: 100, Rows: 30}, Resizes: resizes,
		})
		terminalDone <- terminalResult{code: code, err: execErr}
	}()
	time.Sleep(250 * time.Millisecond)
	resizes <- TerminalSize{Cols: 132, Rows: 44}
	time.Sleep(100 * time.Millisecond)
	if _, err := terminalWriter.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write terminal input: %v", err)
	}
	result := <-terminalDone
	if result.err != nil || result.code != 0 {
		t.Fatalf("terminal exec: code=%d output=%q err=%v", result.code, terminalOutput.String(), result.err)
	}
	terminalText := strings.ReplaceAll(terminalOutput.String(), "\r", "")
	if !strings.Contains(terminalText, "30 100\n") || !strings.Contains(terminalText, "44 132\n") || !strings.Contains(terminalText, "<hello>") {
		t.Fatalf("terminal size/input output = %q", terminalText)
	}

	terminalCancelCtx, cancelTerminal := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelTerminal()
	_, err = engine.ExecTerminalContainer(terminalCancelCtx, container.ID, TerminalExecRequest{
		Command: []string{"sleep", "30"}, Stdin: bytes.NewReader(nil), Output: io.Discard,
		InitialSize: TerminalSize{Cols: 80, Rows: 24}, Resizes: make(chan TerminalSize),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel terminal exec: %v", err)
	}
	if running, inspectErr := engine.InspectContainer(container.ID); inspectErr != nil || running.State != "running" {
		t.Fatalf("terminal cancellation stopped workload: %+v, %v", running, inspectErr)
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
	if len(logs) < 2 || len(logs) > 3 {
		t.Fatalf("expected active plus rotated logs, got %v", logs)
	}
}

func TestStaticInitRunsInGlibcImage(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_RUNTIME_INTEGRATION=1 on an isolated root host")
	}
	config := runtimeIntegrationConfig()
	if err := os.MkdirAll(config.LogRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	image, err := engine.Pull(ctx, PullRequest{Reference: "docker.io/library/debian:13-slim"})
	if err != nil {
		t.Fatalf("pull glibc image: %v", err)
	}
	container, err := engine.CreateContainer(ctx, ContainerSpec{
		ImageID:      image.ID,
		Name:         "platformd-glibc-init",
		Command:      []string{"/bin/sh", "-c", "printf glibc-init-ok"},
		Labels:       map[string]string{"io.platformd.test": "glibc-init"},
		LogPath:      filepath.Join(config.LogRoot, "glibc-init.log"),
		LogSizeBytes: 1024,
		LogMaxFiles:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.RemoveContainer(context.Background(), container.ID, true)
	if err := engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatalf("start glibc container: %v", err)
	}
	if code, err := engine.WaitContainer(ctx, container.ID); err != nil || code != 0 {
		t.Fatalf("wait glibc container: code=%d err=%v", code, err)
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

func TestProjectFirewallPacketPolicy(t *testing.T) {
	if os.Getenv("PLATFORMD_RUNTIME_INTEGRATION") != "1" || os.Getenv("PLATFORMD_FIREWALL_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_RUNTIME_INTEGRATION=1 and PLATFORMD_FIREWALL_INTEGRATION=1 on an isolated root host")
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
	image, err := engine.Pull(ctx, PullRequest{Reference: "docker.io/library/alpine:3.22"})
	if err != nil {
		t.Fatalf("pull image: %v", err)
	}

	projectA := firewall.Project{ID: "packet-a", Bridge: "pdit-a", Subnet: netip.MustParsePrefix("10.89.44.0/24"), Gateway: netip.MustParseAddr("10.89.44.1"), ObjectStoreEnabled: true}
	projectB := firewall.Project{ID: "packet-b", Bridge: "pdit-b", Subnet: netip.MustParsePrefix("10.89.45.0/24"), Gateway: netip.MustParseAddr("10.89.45.1")}
	containerA := createPacketTestContainer(t, ctx, engine, image.ID, projectA)
	containerAPeer := createPacketTestService(t, ctx, engine, image.ID, "packet-a-peer", projectA.ID, projectA.Gateway)
	containerB := createPacketTestContainer(t, ctx, engine, image.ID, projectB)

	manager := firewall.New()
	if err := firewall.EnableIPv4Forwarding(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply([]firewall.Project{projectA, projectB}); err != nil {
		t.Fatalf("publish packet policy: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Clear(); err != nil {
			t.Errorf("clear packet policy: %v", err)
		}
	})

	addressA := net.JoinHostPort(containerA.IPs["packet-a"][0], "8080")
	if err := waitForTCP(ctx, addressA); err != nil {
		t.Fatalf("host-initiated proxy connection: %v", err)
	}

	allowed, err := net.Listen("tcp", net.JoinHostPort(projectA.Gateway.String(), fmt.Sprint(firewall.ObjectStorePort)))
	if err != nil {
		t.Fatalf("listen on allowed gateway port: %v", err)
	}
	defer allowed.Close()
	blocked, err := net.Listen("tcp", net.JoinHostPort(projectA.Gateway.String(), "9001"))
	if err != nil {
		t.Fatalf("listen on blocked gateway port: %v", err)
	}
	defer blocked.Close()
	dnsTCP, err := net.Listen("tcp", net.JoinHostPort(projectA.Gateway.String(), fmt.Sprint(firewall.DNSPort)))
	if err != nil {
		t.Fatalf("listen on DNS TCP port: %v", err)
	}
	defer dnsTCP.Close()
	dnsUDP, err := net.ListenPacket("udp4", net.JoinHostPort(projectA.Gateway.String(), fmt.Sprint(firewall.DNSPort)))
	if err != nil {
		t.Fatalf("listen on DNS UDP port: %v", err)
	}
	defer dnsUDP.Close()

	assertContainerCommandCode(t, ctx, engine, containerA.ID, 0, "nc", "-z", "-w", "3", projectA.Gateway.String(), fmt.Sprint(firewall.ObjectStorePort))
	assertContainerCommandCode(t, ctx, engine, containerA.ID, 0, "nc", "-z", "-w", "3", projectA.Gateway.String(), fmt.Sprint(firewall.DNSPort))
	assertContainerCommandCode(t, ctx, engine, containerA.ID, 1, "nc", "-z", "-w", "2", projectA.Gateway.String(), "9001")
	assertContainerCommandCode(t, ctx, engine, containerA.ID, 0, "nc", "-z", "-w", "3", containerAPeer.IPs["packet-a"][0], "8080")
	assertContainerCommandCode(t, ctx, engine, containerA.ID, 1, "nc", "-z", "-w", "2", containerB.IPs["packet-b"][0], "8080")
	assertContainerCommandCode(t, ctx, engine, containerA.ID, 0, "nc", "-z", "-w", "5", "1.1.1.1", "443")

	udpResult := make(chan error, 1)
	go func() {
		buffer := make([]byte, 16)
		_ = dnsUDP.SetDeadline(time.Now().Add(5 * time.Second))
		length, address, readErr := dnsUDP.ReadFrom(buffer)
		if readErr != nil {
			udpResult <- readErr
			return
		}
		if string(buffer[:length]) != "ping" {
			udpResult <- fmt.Errorf("unexpected UDP payload %q", buffer[:length])
			return
		}
		_, writeErr := dnsUDP.WriteTo([]byte("pong"), address)
		udpResult <- writeErr
	}()
	var udpStdout bytes.Buffer
	udpCode, udpErr := engine.ExecContainer(ctx, containerA.ID, ExecRequest{
		Command: []string{"/bin/sh", "-c", fmt.Sprintf("printf ping | nc -u -w 2 %s %d", projectA.Gateway, firewall.DNSPort)},
		Stdout:  &udpStdout,
	})
	if udpErr != nil || udpCode != 0 || udpStdout.String() != "pong" {
		t.Fatalf("DNS UDP round trip: code=%d stdout=%q err=%v", udpCode, udpStdout.String(), udpErr)
	}
	if err := <-udpResult; err != nil {
		t.Fatalf("DNS UDP listener: %v", err)
	}
}

func createPacketTestContainer(t *testing.T, ctx context.Context, engine *Engine, imageID string, project firewall.Project) Container {
	t.Helper()
	network, err := engine.CreateNetwork(NetworkSpec{
		Name: project.ID, Interface: project.Bridge, Subnet: project.Subnet.String(), Gateway: project.Gateway.String(),
		Labels: map[string]string{"io.platformd.test": "firewall"},
	})
	if err != nil {
		t.Fatalf("create network %s: %v", project.ID, err)
	}
	t.Cleanup(func() {
		if err := engine.RemoveNetwork(network.Name); err != nil {
			t.Errorf("remove network %s: %v", network.Name, err)
		}
	})
	return createPacketTestService(t, ctx, engine, imageID, project.ID, network.Name, project.Gateway)
}

func createPacketTestService(t *testing.T, ctx context.Context, engine *Engine, imageID, name, network string, gateway netip.Addr) Container {
	t.Helper()
	container, err := engine.CreateContainer(ctx, ContainerSpec{
		ImageID:      imageID,
		Name:         "platformd-" + name,
		Command:      []string{"/bin/sh", "-c", "while true; do printf ok | nc -l -p 8080; done"},
		Labels:       map[string]string{"io.platformd.test": "firewall"},
		Network:      network,
		DNSServers:   []string{gateway.String()},
		LogPath:      filepath.Join(runtimeIntegrationConfig().LogRoot, name+".log"),
		LogSizeBytes: 1024,
		LogMaxFiles:  2,
	})
	if err != nil {
		t.Fatalf("create container %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := engine.RemoveContainer(context.Background(), container.ID, true); err != nil {
			t.Errorf("remove container %s: %v", container.ID, err)
		}
	})
	if err := engine.StartContainer(ctx, container.ID); err != nil {
		t.Fatalf("start container %s: %v", name, err)
	}
	container, err = engine.InspectContainer(container.ID)
	if err != nil || len(container.IPs[network]) != 1 {
		t.Fatalf("inspect container %s: %+v, %v", name, container, err)
	}
	return container
}

func waitForTCP(ctx context.Context, address string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func assertContainerCommandCode(t *testing.T, ctx context.Context, engine *Engine, containerID string, expected int, command ...string) {
	t.Helper()
	var stderr bytes.Buffer
	code, err := engine.ExecContainer(ctx, containerID, ExecRequest{Command: command, Stderr: &stderr})
	if err != nil || code != expected {
		t.Fatalf("command %q: code=%d expected=%d stderr=%q err=%v", command, code, expected, stderr.String(), err)
	}
}

func runtimeIntegrationConfig() Config {
	return Config{
		TransientRoot:      integrationRuntimeRoot,
		RunRoot:            filepath.Join(integrationRuntimeRoot, "runroot"),
		GraphRoot:          filepath.Join(integrationDataRoot, "storage"),
		LogRoot:            filepath.Join(integrationDataRoot, "logs"),
		StaticDir:          filepath.Join(integrationRuntimeRoot, "libpod"),
		VolumePath:         filepath.Join(integrationRuntimeRoot, "volumes"),
		NetworkConfigDir:   filepath.Join(integrationRuntimeRoot, "networks"),
		HooksDir:           filepath.Join(integrationRuntimeRoot, "hooks"),
		CDISpecDir:         filepath.Join(integrationRuntimeRoot, "cdi"),
		ContainersConf:     filepath.Join(integrationReleaseRoot, "containers.conf"),
		StorageConf:        filepath.Join(integrationReleaseRoot, "storage.conf"),
		RegistriesConf:     filepath.Join(integrationReleaseRoot, "registries.conf"),
		SignaturePolicy:    filepath.Join(integrationReleaseRoot, "policy.json"),
		SeccompProfile:     filepath.Join(integrationReleaseRoot, "seccomp.json"),
		DefaultMountsFile:  filepath.Join(integrationReleaseRoot, "mounts.conf"),
		OCIRuntime:         filepath.Join(integrationReleaseRoot, "crun"),
		Conmon:             filepath.Join(integrationReleaseRoot, "conmon"),
		CgroupWorkloadRoot: "/workloads",
		AllowedMountRoots:  []string{filepath.Join(integrationDataRoot, "volumes"), filepath.Join(integrationRuntimeRoot, "generated")},
	}
}
