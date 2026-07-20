package cloudflaremesh

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/iivankin/platformd/internal/containerengine"
)

const (
	cloudflareContainer = "platformd-cloudflare-mesh"
	cloudflareStatePath = "/var/lib/cloudflare-warp"
)

type RuntimeEngine interface {
	Build(context.Context, containerengine.BuildRequest) (containerengine.Image, error)
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
	ExecContainer(context.Context, string, containerengine.ExecRequest) (int, error)
}

type ProductionRuntimeConfig struct {
	Engine        RuntimeEngine
	Network       containerengine.Network
	StateRoot     string
	GeneratedRoot string
	LogRoot       string
	CgroupParent  string
	BuildLog      io.Writer
	StartupError  error
}

func (config ProductionRuntimeConfig) validate() error {
	if config.Engine == nil ||
		config.StateRoot == "" || config.GeneratedRoot == "" || config.LogRoot == "" || config.CgroupParent == "" {
		return errors.New("Cloudflare Mesh sidecar runtime configuration is incomplete")
	}
	if config.StartupError == nil && (config.Network.Name == "" || config.Network.Gateway == "") {
		return errors.New("Cloudflare Mesh sidecar network is unavailable")
	}
	return nil
}

func sidecarContainerSpec(config ProductionRuntimeConfig, imageID string) containerengine.ContainerSpec {
	return containerengine.ContainerSpec{
		ImageID: imageID, Name: cloudflareContainer,
		Entrypoint: []string{"/bin/warp-svc"},
		Labels: map[string]string{
			"io.platformd.owner": "system", "io.platformd.component": "cloudflare-mesh",
		},
		Network: config.Network.Name, DNSServers: []string{config.Network.Gateway},
		Mounts:       []containerengine.Mount{{Source: config.StateRoot, Destination: cloudflareStatePath}},
		LogPath:      filepath.Join(config.LogRoot, "infrastructure", "cloudflare-mesh.log"),
		LogSizeBytes: 16 << 20, LogMaxFiles: 3, CgroupParent: config.CgroupParent,
		SecurityProfile: containerengine.ContainerSecurityCloudflareMesh,
	}
}
