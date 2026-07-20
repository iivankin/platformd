package containerengine

import (
	"path/filepath"

	"github.com/iivankin/platformd/internal/layout"
)

func ProductionConfig(paths layout.Paths, cgroupWorkloadRoot string) Config {
	runtimeRoot := filepath.Join(paths.Current, "runtime")
	return Config{
		TransientRoot:      paths.ContainerRoot,
		RunRoot:            filepath.Join(paths.ContainerRoot, "runroot"),
		GraphRoot:          paths.ContainerCache,
		LogRoot:            paths.LogsRoot,
		StaticDir:          filepath.Join(paths.ContainerRoot, "libpod"),
		VolumePath:         filepath.Join(paths.ContainerRoot, "volumes"),
		NetworkConfigDir:   filepath.Join(paths.ContainerRoot, "networks"),
		HooksDir:           filepath.Join(paths.ContainerRoot, "hooks"),
		CDISpecDir:         filepath.Join(paths.ContainerRoot, "cdi"),
		ContainersConf:     filepath.Join(runtimeRoot, "containers.conf"),
		StorageConf:        filepath.Join(runtimeRoot, "storage.conf"),
		RegistriesConf:     filepath.Join(runtimeRoot, "registries.conf"),
		SignaturePolicy:    filepath.Join(runtimeRoot, "policy.json"),
		SeccompProfile:     filepath.Join(runtimeRoot, "seccomp.json"),
		DefaultMountsFile:  filepath.Join(runtimeRoot, "mounts.conf"),
		OCIRuntime:         filepath.Join(runtimeRoot, "crun"),
		Conmon:             filepath.Join(runtimeRoot, "conmon"),
		CgroupWorkloadRoot: cgroupWorkloadRoot,
		AllowedMountRoots: []string{
			paths.VolumesRoot, paths.GeneratedRoot, paths.PostgresExtensionRoot, paths.CloudflareMeshRoot,
		},
	}
}
