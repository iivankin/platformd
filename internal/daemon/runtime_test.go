package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetTransientDirectoryRemovesPreviousContents(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "generated")
	if err := os.MkdirAll(filepath.Join(directory, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "old", "secret"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := resetTransientDirectory(directory); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("transient directory was not reset: %v, %v", entries, err)
	}
	if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("transient directory mode = %v, %v", info, err)
	}
}

func TestResetTransientDirectoryRejectsRoot(t *testing.T) {
	if err := resetTransientDirectory("/"); err == nil {
		t.Fatal("expected filesystem root to be rejected")
	}
}
