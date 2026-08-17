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

func TestReadNetworkFindsInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev")
	content := "" +
		"Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0\n" +
		"  eth0:     100       1    0    0    0     0          0         0      200       2    0    0    0     0       0          0\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := readNetwork(path, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if rx != 100 || tx != 200 {
		t.Fatalf("network counters = rx %d tx %d", rx, tx)
	}
}
