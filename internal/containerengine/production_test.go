package containerengine

import (
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/layout"
)

func TestProductionConfigUsesOnlyPrivateRoots(t *testing.T) {
	root := t.TempDir()
	paths := layout.FromRoots(
		filepath.Join(root, "data"),
		filepath.Join(root, "config"),
		filepath.Join(root, "run"),
		filepath.Join(root, "bin", "platformd"),
		filepath.Join(root, "systemd", "platformd.service"),
	)
	config := ProductionConfig(paths, "/system.slice/platformd.service/workloads")
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.GraphRoot != paths.ContainerCache || config.TransientRoot != paths.ContainerRoot {
		t.Fatalf("unexpected runtime roots: %+v", config)
	}
	if config.ContainersConf != filepath.Join(paths.Current, "runtime", "containers.conf") {
		t.Fatalf("unexpected containers.conf path %q", config.ContainersConf)
	}
	if len(config.AllowedMountRoots) != 3 || config.AllowedMountRoots[2] != paths.PostgresExtensionRoot {
		t.Fatalf("PostgreSQL extension sources are not an allowed private mount root: %v", config.AllowedMountRoots)
	}
}
