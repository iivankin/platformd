//go:build linux && amd64 && cgo

package containerengine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containers/podman/v5/libpod"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/containers/podman/v5/pkg/specgen/generate"
	buildspec "github.com/opencontainers/runtime-spec/specs-go"
	nettypes "go.podman.io/common/libnetwork/types"
)

func (e *Engine) CreateContainer(ctx context.Context, input ContainerSpec) (Container, error) {
	if err := e.validateContainerSpec(input); err != nil {
		return Container{}, err
	}

	no := false
	yes := true
	spec := specgen.NewSpecGenerator(input.ImageID, false)
	spec.Name = input.Name
	spec.Entrypoint = append([]string(nil), input.Entrypoint...)
	spec.Command = append([]string(nil), input.Command...)
	spec.Env = cloneStrings(input.Environment)
	spec.Labels = cloneStrings(input.Labels)
	spec.Terminal = &no
	spec.Stdin = &no
	spec.Init = &yes
	spec.ImageVolumeMode = "ignore"
	spec.NoNewPrivileges = &yes
	spec.Privileged = &no
	spec.SelinuxOpts = []string{"disable"}
	spec.SeccompPolicy = "default"
	spec.SeccompProfilePath = e.config.SeccompProfile
	spec.Systemd = "false"
	spec.SdNotifyMode = define.SdNotifyModeIgnore
	spec.CgroupParent = input.CgroupParent
	spec.CgroupsMode = "enabled"
	spec.LogConfiguration = &specgen.LogConfig{
		Driver: define.KubernetesLogging,
		Path:   input.LogPath,
		Size:   input.LogSizeBytes,
	}

	if input.Network == "" {
		spec.NetNS = specgen.Namespace{NSMode: specgen.NoNetwork}
	} else {
		spec.NetNS = specgen.Namespace{NSMode: specgen.Bridge}
		spec.Networks = map[string]nettypes.PerNetworkOptions{input.Network: {}}
	}
	spec.DNSServers = make([]net.IP, 0, len(input.DNSServers))
	for _, address := range input.DNSServers {
		spec.DNSServers = append(spec.DNSServers, net.ParseIP(address))
	}
	spec.DNSSearch = append([]string(nil), input.DNSSearch...)

	mounts, err := e.runtimeMounts(input.Mounts)
	if err != nil {
		return Container{}, err
	}
	spec.Mounts = mounts
	spec.ResourceLimits = resourceLimits(input.CPUMillicores, input.MemoryMaxBytes)

	warnings, err := generate.CompleteSpec(ctx, e.runtime, spec)
	if err != nil {
		return Container{}, fmt.Errorf("complete container spec: %w", err)
	}
	if len(warnings) > 0 {
		return Container{}, fmt.Errorf("container spec warnings are not accepted: %s", strings.Join(warnings, "; "))
	}
	runtimeSpec, completed, options, err := generate.MakeContainer(ctx, e.runtime, spec, false, nil)
	if err != nil {
		return Container{}, fmt.Errorf("generate OCI container: %w", err)
	}
	options = append(options, libpod.WithLogRotation(input.LogMaxFiles))
	created, err := generate.ExecuteCreate(ctx, e.runtime, runtimeSpec, completed, false, options...)
	if err != nil {
		return Container{}, fmt.Errorf("create container: %w", err)
	}
	return e.publicContainer(created)
}

func (e *Engine) StartContainer(ctx context.Context, id string) error {
	container, err := e.lookupContainer(id)
	if err != nil {
		return err
	}
	if err := container.Start(ctx, false); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

func (e *Engine) StopContainer(id string, timeoutSeconds uint) error {
	container, err := e.lookupContainer(id)
	if err != nil {
		return err
	}
	if err := container.StopWithTimeout(timeoutSeconds); err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

func (e *Engine) KillContainer(id string, signal syscall.Signal) error {
	container, err := e.lookupContainer(id)
	if err != nil {
		return err
	}
	if err := container.Kill(uint(signal)); err != nil {
		return fmt.Errorf("kill container %s: %w", id, err)
	}
	return nil
}

func (e *Engine) WaitContainer(ctx context.Context, id string) (int32, error) {
	container, err := e.lookupContainer(id)
	if err != nil {
		return -1, err
	}
	code, err := container.Wait(ctx)
	if err != nil {
		return -1, fmt.Errorf("wait container %s: %w", id, err)
	}
	if err := container.Cleanup(context.Background(), false); err != nil {
		return code, fmt.Errorf("cleanup exited container %s: %w", id, err)
	}
	return code, nil
}

func (e *Engine) RemoveContainer(ctx context.Context, id string, force bool) error {
	container, err := e.lookupContainer(id)
	if err != nil {
		return err
	}
	if err := e.runtime.RemoveContainer(ctx, container, force, false, nil); err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}

func (e *Engine) InspectContainer(id string) (Container, error) {
	container, err := e.lookupContainer(id)
	if err != nil {
		return Container{}, err
	}
	return e.publicContainer(container)
}

func (e *Engine) ExecContainer(ctx context.Context, id string, request ExecRequest) (int, error) {
	if len(request.Command) == 0 {
		return -1, fmt.Errorf("exec command is empty")
	}
	container, err := e.lookupContainer(id)
	if err != nil {
		return -1, err
	}
	streams := &define.AttachStreams{
		OutputStream: writerOrDiscard(request.Stdout),
		ErrorStream:  writerOrDiscard(request.Stderr),
		AttachOutput: true,
		AttachError:  true,
	}
	config := &libpod.ExecConfig{
		Command:      append([]string(nil), request.Command...),
		Environment:  cloneStrings(request.Environment),
		User:         request.User,
		WorkDir:      request.WorkDir,
		AttachStdout: true,
		AttachStderr: true,
	}
	if request.Stdin != nil {
		streams.InputStream = bufio.NewReader(request.Stdin)
		streams.AttachInput = true
		config.AttachStdin = true
	}
	exitCode, err := container.ExecContext(ctx, config, streams)
	if err != nil {
		return exitCode, fmt.Errorf("exec in container %s: %w", id, err)
	}
	return exitCode, nil
}

func (e *Engine) lookupContainer(id string) (*libpod.Container, error) {
	container, err := e.runtime.LookupContainer(id)
	if err != nil {
		return nil, fmt.Errorf("lookup container %s: %w", id, err)
	}
	return container, nil
}

func (e *Engine) publicContainer(container *libpod.Container) (Container, error) {
	state, err := container.State()
	if err != nil {
		return Container{}, fmt.Errorf("read container state: %w", err)
	}
	result := Container{
		ID:    container.ID(),
		Name:  container.Name(),
		State: state.String(),
		IPs:   make(map[string][]string),
	}
	if pid, err := container.PID(); err == nil {
		result.Pid = pid
	}
	if pid, err := container.ConmonPID(); err == nil {
		result.ConmonPID = pid
	}
	if exitCode, exited, err := container.ExitCode(); err == nil && exited {
		result.ExitCode = exitCode
	}
	status, err := container.GetNetworkStatus()
	if err != nil {
		return Container{}, fmt.Errorf("read container network status: %w", err)
	}
	for network, block := range status {
		for _, networkInterface := range block.Interfaces {
			for _, subnet := range networkInterface.Subnets {
				result.IPs[network] = append(result.IPs[network], subnet.IPNet.IP.String())
			}
		}
	}
	return result, nil
}

func (e *Engine) validateContainerSpec(spec ContainerSpec) error {
	if spec.ImageID == "" || spec.Name == "" {
		return fmt.Errorf("container image ID and name are required")
	}
	if err := validateAbsolutePath("container log", spec.LogPath); err != nil {
		return err
	}
	if !pathWithin(spec.LogPath, e.config.LogRoot) {
		return fmt.Errorf("container log path %s is outside %s", spec.LogPath, e.config.LogRoot)
	}
	if spec.LogSizeBytes <= 0 || spec.LogMaxFiles == 0 {
		return fmt.Errorf("container log size and retained file count must be positive")
	}
	if spec.CPUMillicores < 0 || spec.MemoryMaxBytes < 0 {
		return fmt.Errorf("container resource limits cannot be negative")
	}
	if spec.CgroupParent != "" {
		if err := validateAbsolutePath("container cgroup parent", spec.CgroupParent); err != nil {
			return err
		}
		if !pathWithin(spec.CgroupParent, e.config.CgroupWorkloadRoot) {
			return fmt.Errorf("container cgroup parent must be below %s", e.config.CgroupWorkloadRoot)
		}
	}
	for _, address := range spec.DNSServers {
		if net.ParseIP(address) == nil {
			return fmt.Errorf("invalid DNS server %q", address)
		}
	}
	for _, search := range spec.DNSSearch {
		if search == "" || len(search) > 253 || strings.ContainsAny(search, "\x00 /:") {
			return fmt.Errorf("invalid DNS search domain %q", search)
		}
	}
	return nil
}

func (e *Engine) runtimeMounts(mounts []Mount) ([]buildspec.Mount, error) {
	result := make([]buildspec.Mount, 0, len(mounts))
	destinations := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		if err := validateAbsolutePath("mount source", mount.Source); err != nil {
			return nil, err
		}
		if err := validateAbsolutePath("mount destination", mount.Destination); err != nil {
			return nil, err
		}
		if mount.Destination == "/" {
			return nil, fmt.Errorf("mount destination cannot replace container root")
		}
		if _, exists := destinations[mount.Destination]; exists {
			return nil, fmt.Errorf("duplicate mount destination %s", mount.Destination)
		}
		destinations[mount.Destination] = struct{}{}

		info, err := os.Lstat(mount.Source)
		if err != nil {
			return nil, fmt.Errorf("inspect mount source %s: %w", mount.Source, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("mount source %s must be a regular file or directory, not a symlink", mount.Source)
		}
		resolved, err := filepath.EvalSymlinks(mount.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve mount source %s: %w", mount.Source, err)
		}
		allowed := false
		for _, root := range e.config.AllowedMountRoots {
			if pathWithin(resolved, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("mount source %s is outside managed roots", mount.Source)
		}
		option := "bind"
		if info.IsDir() {
			option = "rbind"
		}
		access := "rw"
		if mount.ReadOnly {
			access = "ro"
		}
		result = append(result, buildspec.Mount{
			Destination: mount.Destination,
			Type:        "bind",
			Source:      resolved,
			Options:     []string{option, access, "rprivate"},
		})
	}
	return result, nil
}

func resourceLimits(cpuMillicores, memoryBytes int64) *buildspec.LinuxResources {
	if cpuMillicores == 0 && memoryBytes == 0 {
		return nil
	}
	resources := &buildspec.LinuxResources{}
	if cpuMillicores > 0 {
		period := uint64(100_000)
		quota := cpuMillicores * int64(period) / 1000
		resources.CPU = &buildspec.LinuxCPU{Period: &period, Quota: &quota}
	}
	if memoryBytes > 0 {
		resources.Memory = &buildspec.LinuxMemory{Limit: &memoryBytes}
	}
	return resources
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}
