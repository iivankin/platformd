package layout

import "path/filepath"

type Paths struct {
	DataRoot      string
	ConfigRoot    string
	StateDatabase string
	MasterKey     string
	ReleasesRoot  string
	Current       string
	Previous      string
	LocalBinary   string
	UnitFile      string
}

func Production() Paths {
	return FromRoots("/var/lib/platformd", "/etc/platformd", "/usr/local/bin/platformd", "/etc/systemd/system/platformd.service")
}

func FromRoots(dataRoot, configRoot, localBinary, unitFile string) Paths {
	releasesRoot := filepath.Join(dataRoot, "releases")
	return Paths{
		DataRoot:      dataRoot,
		ConfigRoot:    configRoot,
		StateDatabase: filepath.Join(dataRoot, "state", "platformd.db"),
		MasterKey:     filepath.Join(configRoot, "master.key"),
		ReleasesRoot:  releasesRoot,
		Current:       filepath.Join(releasesRoot, "current"),
		Previous:      filepath.Join(releasesRoot, "previous"),
		LocalBinary:   localBinary,
		UnitFile:      unitFile,
	}
}
