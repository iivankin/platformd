package fsusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryBytesCountsRegularFilesWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "one"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "two"), []byte("4567"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(external, make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}

	bytes, err := DirectoryBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if bytes != 7 {
		t.Fatalf("directory bytes = %d, want 7", bytes)
	}
}
