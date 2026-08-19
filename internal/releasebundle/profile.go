package releasebundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The v2 archive profile is intentionally fixed so an older v2 binary never
// has to interpret new helper or configuration names from a signed update.
var runtimeProfile = []struct {
	path string
	mode uint32
}{
	{path: "runtime/catatonit", mode: 0o755},
	{path: "runtime/conmon", mode: 0o755},
	{path: "runtime/containers.conf", mode: 0o644},
	{path: "runtime/crun", mode: 0o755},
	{path: "runtime/mounts.conf", mode: 0o644},
	{path: "runtime/netavark", mode: 0o755},
	{path: "runtime/platformd-objectstore", mode: 0o755},
	{path: "runtime/platformd-telemetry", mode: 0o755},
	{path: "runtime/policy.json", mode: 0o644},
	{path: "runtime/registries.conf", mode: 0o644},
	{path: "runtime/seccomp.json", mode: 0o644},
	{path: "runtime/storage.conf", mode: 0o644},
}

var workerRuntimeProfile = []struct {
	path string
	mode uint32
}{
	{path: "runtime/catatonit", mode: 0o755},
	{path: "runtime/conmon", mode: 0o755},
	{path: "runtime/containers.conf", mode: 0o644},
	{path: "runtime/crun", mode: 0o755},
	{path: "runtime/mounts.conf", mode: 0o644},
	{path: "runtime/netavark", mode: 0o755},
	{path: "runtime/policy.json", mode: 0o644},
	{path: "runtime/registries.conf", mode: 0o644},
	{path: "runtime/seccomp.json", mode: 0o644},
	{path: "runtime/storage.conf", mode: 0o644},
}

func validateRuntimeProfile(files []ManifestFile) error {
	if matchesRuntimeProfile(files, runtimeProfile) || matchesRuntimeProfile(files, workerRuntimeProfile) {
		return nil
	}
	return fmt.Errorf("runtime bundle v2 profile requires the control-plane or worker file set")
}

func matchesRuntimeProfile(files []ManifestFile, profile []struct {
	path string
	mode uint32
}) bool {
	if len(files) != len(profile) {
		return false
	}
	for index, expected := range profile {
		actual := files[index]
		if actual.Path != expected.path || actual.Mode != expected.mode {
			return false
		}
	}
	return true
}

func RuntimeHelperPaths(root string) ([]string, error) {
	if root == "" {
		return nil, errors.New("runtime root is required")
	}
	paths := make([]string, 0, len(runtimeProfile))
	for _, entry := range runtimeProfile {
		if entry.mode != 0o755 {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(entry.path))
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, errors.New("runtime helpers are missing")
	}
	return paths, nil
}
