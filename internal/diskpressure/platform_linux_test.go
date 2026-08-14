//go:build linux

package diskpressure

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStatfsCollectorSeparatesUsedAndAvailableBytes(t *testing.T) {
	var stat unix.Statfs_t
	path := t.TempDir()
	if err := unix.Statfs(path, &stat); err != nil {
		t.Fatal(err)
	}
	usage, err := (StatfsCollector{}).Collect(path)
	if err != nil {
		t.Fatal(err)
	}
	blockSize := uint64(stat.Bsize)
	if want := (stat.Blocks - stat.Bfree) * blockSize; usage.UsedBytes != want {
		t.Fatalf("used bytes = %d, want %d", usage.UsedBytes, want)
	}
	if want := stat.Bavail * blockSize; usage.AvailableBytes != want {
		t.Fatalf("available bytes = %d, want %d", usage.AvailableBytes, want)
	}
}

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
