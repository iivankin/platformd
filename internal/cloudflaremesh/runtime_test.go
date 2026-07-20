package cloudflaremesh

import (
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
)

func TestSidecarContainerSpecIsIsolatedAndPersistent(t *testing.T) {
	config := ProductionRuntimeConfig{
		Network:   containerengine.Network{Name: "mesh-network", Gateway: "10.80.0.1"},
		StateRoot: "/var/lib/platformd/cloudflare-mesh", LogRoot: "/var/lib/platformd/logs",
		CgroupParent: "/platformd/cloudflare-mesh",
	}
	spec := sidecarContainerSpec(config, "image")
	if spec.ImageID != "image" || spec.Name != cloudflareContainer || spec.Network != "mesh-network" ||
		spec.SecurityProfile != containerengine.ContainerSecurityCloudflareMesh {
		t.Fatalf("sidecar container spec = %+v", spec)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != config.StateRoot ||
		spec.Mounts[0].Destination != cloudflareStatePath {
		t.Fatalf("sidecar state mount = %+v", spec.Mounts)
	}
	if len(spec.DNSServers) != 1 || spec.DNSServers[0] != config.Network.Gateway || spec.CPUMillicores != 0 || spec.MemoryMaxBytes != 0 {
		t.Fatalf("sidecar network or resource configuration = %+v", spec)
	}
}
