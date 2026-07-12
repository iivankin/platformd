//go:build linux && amd64 && cgo

package containerengine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRuntimeMountsPreserveExplicitReadOnlyAccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "redis.conf")
	if err := os.WriteFile(source, []byte("daemonize no\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{config: Config{AllowedMountRoots: []string{root}}}
	mounts, err := engine.runtimeMounts([]Mount{{
		Source: source, Destination: "/run/platformd/redis.conf", ReadOnly: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || !slices.Contains(mounts[0].Options, "ro") || slices.Contains(mounts[0].Options, "rw") {
		t.Fatalf("read-only mount options = %+v", mounts)
	}
}
