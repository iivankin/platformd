package cgroupstats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderReturnsHostCapacityAndRunningResourceCounters(t *testing.T) {
	t.Parallel()
	mountRoot := t.TempDir()
	resourceRoot := filepath.Join(mountRoot, "unit", "workloads", "service-api")
	if err := os.MkdirAll(resourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"cgroup.events":  "populated 1\nfrozen 0\n",
		"cpu.stat":       "usage_usec 123456\nuser_usec 120000\n",
		"memory.current": "987654\n",
	} {
		if err := os.WriteFile(filepath.Join(resourceRoot, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := New(Config{
		MountRoot: mountRoot, WorkloadPath: "/unit/workloads",
		Capacity: func() (int, uint64, error) { return 8, 16 << 30, nil },
		Now:      func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	})
	if err != nil {
		t.Fatal(err)
	}
	sample, err := reader.Read(Service, "api")
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Running || sample.CPUUsageMicros != 123_456 || sample.MemoryBytes != 987_654 ||
		sample.HostCPUCores != 8 || sample.HostMemoryBytes != 16<<30 || sample.ObservedAtMillis != 1_700_000_000_000 {
		t.Fatalf("sample = %+v", sample)
	}
	stopped, err := reader.Read(Redis, "cache")
	if err != nil || stopped.Running || stopped.HostCPUCores != 8 {
		t.Fatalf("stopped sample = %+v, %v", stopped, err)
	}
}

func TestReaderRejectsPathTraversalAndMalformedCounters(t *testing.T) {
	t.Parallel()
	mountRoot := t.TempDir()
	reader, err := New(Config{
		MountRoot: mountRoot, WorkloadPath: "/unit/workloads",
		Capacity: func() (int, uint64, error) { return 1, 1, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, resourceID := range []string{"", "../api", "api/child"} {
		if _, err := reader.Read(Service, resourceID); err == nil {
			t.Fatalf("resource ID %q was accepted", resourceID)
		}
	}
	resourceRoot := filepath.Join(mountRoot, "unit", "workloads", "postgres-db")
	if err := os.MkdirAll(resourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourceRoot, "cgroup.events"), []byte("populated maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(Postgres, "db"); err == nil {
		t.Fatal("malformed populated flag was accepted")
	}
}
