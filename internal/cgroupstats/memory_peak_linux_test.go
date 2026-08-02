//go:build linux

package cgroupstats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReaderUsesResettableMemoryPeakAndClosesItWhenResourceStops(t *testing.T) {
	mountRoot := t.TempDir()
	resourceRoot := filepath.Join(mountRoot, "unit", "workloads", "service-api")
	if err := os.MkdirAll(resourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"cgroup.events":  "populated 1\n",
		"cpu.stat":       "usage_usec 1\n",
		"memory.current": "100\n",
		"memory.peak":    "250\n",
	} {
		if err := os.WriteFile(filepath.Join(resourceRoot, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := New(Config{
		MountRoot: mountRoot, WorkloadPath: "/unit/workloads",
		Capacity: func() (int, uint64, error) { return 1, 1_000, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	sample, err := reader.Read(Service, "api")
	if err != nil || sample.MemoryPeakBytes != 250 {
		t.Fatalf("memory peak sample = %+v, %v", sample, err)
	}
	if len(reader.peaks.files) != 1 {
		t.Fatalf("open peak files = %d", len(reader.peaks.files))
	}
	if err := os.WriteFile(filepath.Join(resourceRoot, "cgroup.events"), []byte("populated 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(Service, "api"); err != nil {
		t.Fatal(err)
	}
	if len(reader.peaks.files) != 0 {
		t.Fatalf("stopped resource retained %d peak files", len(reader.peaks.files))
	}
}
