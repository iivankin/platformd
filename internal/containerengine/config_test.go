package containerengine

import "testing"

func TestConfigRejectsRelativePath(t *testing.T) {
	config := validConfig()
	config.GraphRoot = "relative"
	if err := config.Validate(); err == nil {
		t.Fatal("expected relative graph root to fail")
	}
}

func TestConfigContainsEphemeralPaths(t *testing.T) {
	config := validConfig()
	config.StaticDir = "/run/platformd-other/libpod"
	if err := config.Validate(); err == nil {
		t.Fatal("expected static dir outside transient root to fail")
	}
}

func TestConfigKeepsPersistentPathsOutsideEphemeralRoot(t *testing.T) {
	config := validConfig()
	config.GraphRoot = "/run/platformd/containers/storage"
	if err := config.Validate(); err == nil {
		t.Fatal("expected graph root inside transient root to fail")
	}
}

func TestPathWithinRequiresDescendant(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/var/lib/platformd/volumes/project/volume", want: true},
		{path: "/var/lib/platformd/volumes", want: false},
		{path: "/var/lib/platformd/other", want: false},
		{path: "/var/lib/platformd/volumes-elsewhere/value", want: false},
	} {
		if got := pathWithin(test.path, "/var/lib/platformd/volumes"); got != test.want {
			t.Errorf("pathWithin(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func validConfig() Config {
	return Config{
		TransientRoot:     "/run/platformd/containers",
		RunRoot:           "/run/platformd/containers/runroot",
		GraphRoot:         "/var/lib/platformd/containers/storage",
		LogRoot:           "/var/lib/platformd/logs",
		StaticDir:         "/run/platformd/containers/libpod",
		VolumePath:        "/run/platformd/containers/volumes",
		NetworkConfigDir:  "/run/platformd/containers/networks",
		HooksDir:          "/run/platformd/containers/hooks",
		CDISpecDir:        "/run/platformd/containers/cdi",
		ContainersConf:    "/var/lib/platformd/releases/current/runtime/containers.conf",
		StorageConf:       "/var/lib/platformd/releases/current/runtime/storage.conf",
		RegistriesConf:    "/var/lib/platformd/releases/current/runtime/registries.conf",
		SignaturePolicy:   "/var/lib/platformd/releases/current/runtime/policy.json",
		SeccompProfile:    "/var/lib/platformd/releases/current/runtime/seccomp.json",
		DefaultMountsFile: "/var/lib/platformd/releases/current/runtime/mounts.conf",
		OCIRuntime:        "/var/lib/platformd/releases/current/runtime/crun",
		Conmon:            "/var/lib/platformd/releases/current/runtime/conmon",
		AllowedMountRoots: []string{"/var/lib/platformd/volumes", "/run/platformd/generated"},
	}
}
