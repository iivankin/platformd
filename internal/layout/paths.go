package layout

import "path/filepath"

type Paths struct {
	DataRoot              string
	ConfigRoot            string
	RuntimeRoot           string
	StateDatabase         string
	MasterKey             string
	ReleasesRoot          string
	Current               string
	Previous              string
	ContainerRoot         string
	ContainerCache        string
	GeneratedRoot         string
	LogsRoot              string
	VolumesRoot           string
	RegistryRoot          string
	ObjectsRoot           string
	BackupWorkRoot        string
	PostgresExtensionRoot string
	ReserveFile           string
	DaemonLock            string
	LocalBinary           string
	UnitFile              string
}

func Production() Paths {
	return FromRoots("/var/lib/platformd", "/etc/platformd", "/run/platformd", "/usr/local/bin/platformd", "/etc/systemd/system/platformd.service")
}

func FromRoots(dataRoot, configRoot, runtimeRoot, localBinary, unitFile string) Paths {
	releasesRoot := filepath.Join(dataRoot, "releases")
	return Paths{
		DataRoot:              dataRoot,
		ConfigRoot:            configRoot,
		RuntimeRoot:           runtimeRoot,
		StateDatabase:         filepath.Join(dataRoot, "state", "platformd.db"),
		MasterKey:             filepath.Join(configRoot, "master.key"),
		ReleasesRoot:          releasesRoot,
		Current:               filepath.Join(releasesRoot, "current"),
		Previous:              filepath.Join(releasesRoot, "previous"),
		ContainerRoot:         filepath.Join(runtimeRoot, "containers"),
		ContainerCache:        filepath.Join(dataRoot, "containers", "storage"),
		GeneratedRoot:         filepath.Join(runtimeRoot, "generated"),
		LogsRoot:              filepath.Join(dataRoot, "logs"),
		VolumesRoot:           filepath.Join(dataRoot, "volumes"),
		RegistryRoot:          filepath.Join(dataRoot, "registry"),
		ObjectsRoot:           filepath.Join(dataRoot, "objects"),
		BackupWorkRoot:        filepath.Join(dataRoot, "backups", "work"),
		PostgresExtensionRoot: filepath.Join(dataRoot, "postgres-extensions"),
		ReserveFile:           filepath.Join(dataRoot, ".reserve"),
		DaemonLock:            filepath.Join(runtimeRoot, "locks", "daemon.lock"),
		LocalBinary:           localBinary,
		UnitFile:              unitFile,
	}
}
