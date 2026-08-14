//go:build linux

package diskusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPathBytesDoesNotCrossFilesystemMounts(t *testing.T) {
	root := t.TempDir()
	rootFile := filepath.Join(root, "owned")
	if err := os.WriteFile(rootFile, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	mountpoint := filepath.Join(root, "mounted")
	if err := os.Mkdir(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", mountpoint, "tmpfs", 0, "size=2m"); err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skip("mount capability is unavailable")
		}
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Unmount(mountpoint, 0); err != nil {
			t.Errorf("unmount fixture: %v", err)
		}
	}()
	if err := os.WriteFile(filepath.Join(mountpoint, "foreign"), make([]byte, 1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}

	usage, err := pathBytes(context.Background(), root, make(map[fileIdentity]struct{}), newScanPacer(0, time.Now))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := allocatedBytes(info); usage != want {
		t.Fatalf("path usage = %d, want owned filesystem bytes %d", usage, want)
	}
}
