//go:build linux

package hostmetrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCPUUnitsDoesNotDoubleCountGuestTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, []byte("cpu 100 20 30 400 50 6 7 8 9 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	total, idle, err := readCPUUnits(path)
	if err != nil {
		t.Fatal(err)
	}
	if total != 621 || idle != 450 {
		t.Fatalf("CPU units = total %d idle %d", total, idle)
	}
}
