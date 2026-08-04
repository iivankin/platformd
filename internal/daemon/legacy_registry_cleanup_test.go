package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacyRegistryDataKeepsSiblingState(t *testing.T) {
	root := t.TempDir()
	registry := filepath.Join(root, "registry", "blobs", "payload")
	sibling := filepath.Join(root, "state", "platformd.db")
	if err := os.MkdirAll(filepath.Dir(registry), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sibling), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeLegacyRegistryData(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "registry")); !os.IsNotExist(err) {
		t.Fatalf("legacy registry still exists: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling state was removed: %v", err)
	}
}
