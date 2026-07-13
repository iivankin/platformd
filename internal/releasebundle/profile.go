package releasebundle

import (
	"errors"
	"fmt"
	"path/filepath"
)

// The v1 archive profile is intentionally fixed so an older v1 binary never
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
	{path: "runtime/policy.json", mode: 0o644},
	{path: "runtime/registries.conf", mode: 0o644},
	{path: "runtime/seccomp.json", mode: 0o644},
	{path: "runtime/storage.conf", mode: 0o644},
}

func validateRuntimeProfile(files []ManifestFile) error {
	if len(files) != len(runtimeProfile) {
		return fmt.Errorf("runtime bundle v1 profile requires %d files", len(runtimeProfile))
	}
	for index, expected := range runtimeProfile {
		actual := files[index]
		if actual.Path != expected.path {
			return fmt.Errorf("runtime bundle v1 profile entry %d is %q, expected %q", index, actual.Path, expected.path)
		}
		if actual.Mode != expected.mode {
			return fmt.Errorf("runtime bundle v1 profile file %q has mode %04o, expected %04o", actual.Path, actual.Mode, expected.mode)
		}
	}
	return nil
}

func RuntimeHelperPaths(root string) ([]string, error) {
	if root == "" {
		return nil, errors.New("runtime root is required")
	}
	paths := make([]string, 0, 4)
	for _, entry := range runtimeProfile {
		if entry.mode == 0o755 {
			paths = append(paths, filepath.Join(root, filepath.FromSlash(entry.path)))
		}
	}
	return paths, nil
}
