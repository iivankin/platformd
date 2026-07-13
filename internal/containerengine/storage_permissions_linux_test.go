//go:build linux

package containerengine

import (
	"os"
	"path/filepath"
	"testing"

	"go.podman.io/storage"
)

func TestRepairImageLayerPermissionsRepairsOnlyMaskedLayerRoots(t *testing.T) {
	graphRoot := t.TempDir()
	masked := imageLayerDiffRoot(t, graphRoot, "masked", 0o500)
	unchanged := imageLayerDiffRoot(t, graphRoot, "unchanged", 0o555)

	repaired, err := repairImageLayerPermissions(graphRoot, []storage.Layer{{ID: "masked"}, {ID: "unchanged"}})
	if err != nil || repaired != 1 {
		t.Fatalf("repair result = %d, %v", repaired, err)
	}
	assertMode(t, masked, 0o555)
	assertMode(t, unchanged, 0o555)
}

func TestRepairImageLayerPermissionsRejectsUnsafeLayerID(t *testing.T) {
	if _, err := repairImageLayerPermissions(t.TempDir(), []storage.Layer{{ID: "../outside"}}); err == nil {
		t.Fatal("unsafe layer ID was accepted")
	}
}

func imageLayerDiffRoot(t *testing.T, graphRoot, id string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(graphRoot, "overlay", id, "diff")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s mode = %04o, want %04o", path, actual, expected)
	}
}
