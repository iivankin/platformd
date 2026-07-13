package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeReleaseRuntimeExecutesEveryRequiredHelper(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"catatonit", "conmon", "crun", "netavark"} {
		if err := os.WriteFile(filepath.Join(runtimeRoot, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := probeReleaseRuntime(root); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(runtimeRoot, "conmon"), []byte("#!/bin/sh\necho missing-library >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := probeReleaseRuntime(root)
	if err == nil || !strings.Contains(err.Error(), "conmon") || !strings.Contains(err.Error(), "missing-library") {
		t.Fatalf("incompatible helper error = %v", err)
	}
}
