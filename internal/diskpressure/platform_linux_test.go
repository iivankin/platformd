//go:build linux

package diskpressure

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFileReserveIsAllocatedAndRemoved(t *testing.T) {
	t.Parallel()

	reserve, err := NewFileReserve(os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".reserve")
	const size = int64(8 << 20)
	if err := reserve.Ensure(path, size); err != nil {
		t.Fatal(err)
	}
	present, err := reserve.Present(path, size)
	if err != nil || !present {
		t.Fatalf("reserve present = %v, %v", present, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks*512 < size {
		t.Fatalf("reserve is sparse: blocks=%d size=%d", stat.Blocks, size)
	}
	if err := reserve.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reserve remained: %v", err)
	}
}
