//go:build linux && amd64 && cgo

package cloudflaremesh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
)

const (
	cloudflareWarpVersion  = "2026.6.836.0"
	cloudflarePackageURL   = "https://pkg.cloudflareclient.com/pool/trixie/main/c/cloudflare-warp/cloudflare-warp_2026.6.836.0_amd64.deb"
	cloudflarePackageSHA   = "bfd0f9dac4cfbd55a1e9c684c9927feaee9e310029021ec7e0d780ff6ec82d5b"
	cloudflareBaseImage    = "docker.io/library/debian@sha256:9bb8a3626890e084ab54e888fdd7c4b6d2f119071cd4c5dc5fecb4d73062aa5f"
	cloudflareImage        = "localhost/platformd/cloudflare-mesh:" + cloudflareWarpVersion
	cloudflareImageLabel   = "io.platformd.cloudflare-warp-version"
	cloudflareBuildTimeout = 15 * time.Minute
)

type productionRuntime struct {
	mu          sync.Mutex
	client      *http.Client
	config      ProductionRuntimeConfig
	containerID string
}

func ProductionRuntime(config ProductionRuntimeConfig) (Runtime, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &productionRuntime{
		client: &http.Client{Timeout: 90 * time.Second}, config: config,
	}, nil
}

func (runtime *productionRuntime) Ensure(ctx context.Context, token string, reenroll bool) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		return errors.New("Cloudflare Mesh node token is empty")
	}
	if runtime.config.StartupError != nil {
		return fmt.Errorf("Cloudflare Mesh sidecar network is unavailable: %w", runtime.config.StartupError)
	}
	if err := disableLegacyHostClient(ctx); err != nil {
		return err
	}
	if reenroll && runtime.containerID != "" {
		// Stop the daemon before replacing its persistent registration. Removing
		// an active bind mount would leave warp-svc using deleted inodes.
		if err := runtime.removeContainerLocked(ctx); err != nil {
			return fmt.Errorf("stop Cloudflare Mesh sidecar before re-enrollment: %w", err)
		}
	}
	if err := runtime.prepareState(reenroll); err != nil {
		return err
	}
	image, err := runtime.ensureImage(ctx)
	if err != nil {
		return err
	}
	if runtime.containerID != "" {
		current, inspectErr := runtime.config.Engine.InspectContainer(runtime.containerID)
		if inspectErr == nil && current.State == "running" && !reenroll {
			_ = runtime.runCLI(ctx, "connect")
			if runtime.waitForAddressLocked(ctx, 10*time.Second) == nil {
				return nil
			}
		}
		_ = runtime.removeContainerLocked(ctx)
	}

	created, err := runtime.config.Engine.CreateContainer(ctx, sidecarContainerSpec(runtime.config, image.ID))
	if err != nil {
		return fmt.Errorf("create Cloudflare Mesh sidecar: %w", err)
	}
	runtime.containerID = created.ID
	if err := runtime.config.Engine.StartContainer(ctx, created.ID); err != nil {
		_ = runtime.removeContainerLocked(context.Background())
		return fmt.Errorf("start Cloudflare Mesh sidecar: %w", err)
	}
	if err := runtime.waitForDaemonLocked(ctx, 20*time.Second); err != nil {
		return err
	}
	if !reenroll {
		_ = runtime.runCLI(ctx, "connect")
		if runtime.waitForAddressLocked(ctx, 10*time.Second) == nil {
			return nil
		}
	}
	_, _ = runtime.runCLICaptured(ctx, "registration", "delete")
	if err := runtime.runSecretCLI(ctx, "connector", "new", token); err != nil {
		return err
	}
	if err := runtime.runCLI(ctx, "connect"); err != nil {
		return err
	}
	return runtime.waitForAddressLocked(ctx, 60*time.Second)
}

func (runtime *productionRuntime) Address() (NetworkAddress, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runtime.addressLocked(ctx)
}

func (runtime *productionRuntime) Close() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runtime.removeContainerLocked(ctx)
}

func (runtime *productionRuntime) prepareState(reset bool) error {
	if reset {
		if err := removeManagedDirectory(runtime.config.StateRoot); err != nil {
			return fmt.Errorf("reset Cloudflare Mesh client state: %w", err)
		}
	}
	if err := os.MkdirAll(runtime.config.StateRoot, 0o700); err != nil {
		return fmt.Errorf("create Cloudflare Mesh client state: %w", err)
	}
	return os.Chmod(runtime.config.StateRoot, 0o700)
}

func (runtime *productionRuntime) ensureImage(ctx context.Context) (containerengine.Image, error) {
	if image, err := runtime.config.Engine.InspectImage(ctx, cloudflareImage); err == nil &&
		image.Labels[cloudflareImageLabel] == cloudflareWarpVersion {
		return image, nil
	}
	contextRoot := filepath.Join(runtime.config.GeneratedRoot, "cloudflare-mesh-image")
	if err := removeManagedDirectory(contextRoot); err != nil {
		return containerengine.Image{}, err
	}
	if err := os.MkdirAll(contextRoot, 0o700); err != nil {
		return containerengine.Image{}, err
	}
	packagePath := filepath.Join(contextRoot, "cloudflare-warp.deb")
	if err := runtime.downloadPackage(ctx, packagePath); err != nil {
		return containerengine.Image{}, err
	}
	dockerfile := filepath.Join(contextRoot, "Dockerfile")
	contents := fmt.Sprintf(`FROM %s
COPY cloudflare-warp.deb /tmp/cloudflare-warp.deb
RUN printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d \
 && chmod 0755 /usr/sbin/policy-rc.d \
 && apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends /tmp/cloudflare-warp.deb \
 && rm -rf /var/lib/apt/lists/* /tmp/cloudflare-warp.deb /usr/sbin/policy-rc.d \
 && mkdir -p /var/lib/cloudflare-warp /run/cloudflare-warp /var/log/cloudflare-warp
LABEL io.platformd.owner="system" %s="%s"
STOPSIGNAL SIGTERM
ENTRYPOINT ["/bin/warp-svc"]
`, cloudflareBaseImage, cloudflareImageLabel, cloudflareWarpVersion)
	if err := writeManagedFile(dockerfile, []byte(contents), 0o600); err != nil {
		return containerengine.Image{}, err
	}
	logWriter := runtime.config.BuildLog
	var logFile *os.File
	if logWriter == nil {
		if err := os.MkdirAll(filepath.Join(runtime.config.LogRoot, "infrastructure"), 0o700); err != nil {
			return containerengine.Image{}, err
		}
		var err error
		logFile, err = os.OpenFile(
			filepath.Join(runtime.config.LogRoot, "infrastructure", "cloudflare-mesh-build.log"),
			os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600,
		)
		if err != nil {
			return containerengine.Image{}, err
		}
		defer logFile.Close()
		logWriter = logFile
	}
	image, err := runtime.config.Engine.Build(ctx, containerengine.BuildRequest{
		ContextDirectory: contextRoot, Dockerfile: dockerfile, Reference: cloudflareImage,
		Network: runtime.config.BuildNetwork, Timeout: cloudflareBuildTimeout, Log: logWriter,
	})
	if err != nil {
		return containerengine.Image{}, fmt.Errorf("build Cloudflare Mesh sidecar image: %w", err)
	}
	return image, nil
}

func (runtime *productionRuntime) downloadPackage(ctx context.Context, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudflarePackageURL, nil)
	if err != nil {
		return err
	}
	response, err := runtime.client.Do(request)
	if err != nil {
		return fmt.Errorf("download Cloudflare WARP package: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Cloudflare WARP package: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 80<<20))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != cloudflarePackageSHA {
		return errors.New("Cloudflare WARP package checksum changed")
	}
	return writeManagedFile(path, data, 0o600)
}

func (runtime *productionRuntime) waitForDaemonLocked(ctx context.Context, timeout time.Duration) error {
	return runtime.waitLocked(ctx, timeout, func() error {
		_, err := runtime.runCLICaptured(ctx, "status")
		return err
	}, "Cloudflare Mesh sidecar did not become ready")
}

func (runtime *productionRuntime) waitForAddressLocked(ctx context.Context, timeout time.Duration) error {
	return runtime.waitLocked(ctx, timeout, func() error {
		_, err := runtime.addressLocked(ctx)
		return err
	}, "Cloudflare Mesh did not expose an IPv4 address")
}

func (runtime *productionRuntime) waitLocked(ctx context.Context, timeout time.Duration, check func() error, message string) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastError error
	for {
		if err := check(); err == nil {
			return nil
		} else {
			lastError = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s: %w", message, lastError)
		case <-ticker.C:
		}
	}
}

func (runtime *productionRuntime) addressLocked(ctx context.Context) (NetworkAddress, error) {
	if runtime.containerID == "" {
		return NetworkAddress{}, errors.New("Cloudflare Mesh sidecar is not running")
	}
	container, err := runtime.config.Engine.InspectContainer(runtime.containerID)
	if err != nil || container.State != "running" || container.Pid <= 0 {
		return NetworkAddress{}, errors.New("Cloudflare Mesh sidecar is not running")
	}
	output, err := runtime.execCaptured(ctx, "ip", "-o", "-4", "address", "show", "dev", "CloudflareWARP")
	if err != nil {
		return NetworkAddress{}, err
	}
	return parseAddressOutput(output, container.Pid)
}

func (runtime *productionRuntime) runCLI(ctx context.Context, arguments ...string) error {
	_, err := runtime.runCLICaptured(ctx, arguments...)
	return err
}

func (runtime *productionRuntime) runCLICaptured(ctx context.Context, arguments ...string) (string, error) {
	return runtime.execCaptured(ctx, append([]string{"warp-cli", "--accept-tos"}, arguments...)...)
}

func (runtime *productionRuntime) runSecretCLI(ctx context.Context, arguments ...string) error {
	command := append([]string{"warp-cli", "--accept-tos"}, arguments...)
	code, err := runtime.config.Engine.ExecContainer(ctx, runtime.containerID, containerengine.ExecRequest{Command: command})
	if err != nil || code != 0 {
		// Cloudflare only accepts connector tokens through argv. Never echo the
		// command or daemon output because both may contain registration data.
		return errors.New("Cloudflare Mesh sidecar rejected the protected connector token")
	}
	return nil
}

func (runtime *productionRuntime) execCaptured(ctx context.Context, command ...string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := runtime.config.Engine.ExecContainer(ctx, runtime.containerID, containerengine.ExecRequest{
		Command: command, Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("Cloudflare Mesh sidecar command exited with code %d: %s", code, boundedOutput(stderr.Bytes()))
	}
	return stdout.String(), nil
}

func (runtime *productionRuntime) removeContainerLocked(ctx context.Context) error {
	if runtime.containerID == "" {
		return nil
	}
	id := runtime.containerID
	runtime.containerID = ""
	_ = runtime.config.Engine.StopContainer(id, 10)
	return runtime.config.Engine.RemoveContainer(ctx, id, true)
}

func disableLegacyHostClient(ctx context.Context) error {
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.CommandContext(ctx, "systemctl", "disable", "--now", "warp-svc.service").Run()
		if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "warp-svc.service").Run(); err == nil {
			return errors.New("legacy host Cloudflare WARP service is still active")
		}
	}
	if err := os.Remove("/etc/sysctl.d/99-zzz-cloudflare-warp-connector.conf"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy Cloudflare WARP forwarding config: %w", err)
	}
	return nil
}

func removeManagedDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return fmt.Errorf("unsafe managed directory %q", path)
	}
	return os.RemoveAll(path)
}

func writeManagedFile(path string, value []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".platformd-managed-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(value)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func boundedOutput(output []byte) string {
	const maximum = 32 << 10
	if len(output) > maximum {
		output = output[len(output)-maximum:]
	}
	return strings.TrimSpace(string(output))
}
