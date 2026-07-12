package hostcheck_test

import (
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/hostcheck"
)

func TestSupportedDebianFacts(t *testing.T) {
	t.Parallel()

	facts := validFacts()
	if err := facts.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAllowsExistingListener(t *testing.T) {
	t.Parallel()

	facts := validFacts()
	facts.Port443Available = false
	if err := facts.ValidateForRepair(); err != nil {
		t.Fatal(err)
	}
}

func TestFactsReportAllMissingHostContracts(t *testing.T) {
	t.Parallel()

	facts := hostcheck.Facts{
		OperatingSystem:   "linux",
		Architecture:      "arm64",
		DistributionID:    "debian",
		DistributionMajor: 12,
		EffectiveUID:      1000,
		SystemdVersion:    252,
		FilesystemBytes:   20 << 30,
		FreeBytes:         512 << 20,
	}
	err := facts.Validate()
	if err == nil {
		t.Fatal("invalid host was accepted")
	}
	message := err.Error()
	for _, expected := range []string{"root", "arm64", "debian 12", "systemd 252", "cgroup v2", `controller "cpu"`, "OverlayFS", "nftables", "clock", "port 443", "free disk"} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q does not contain %q", message, expected)
		}
	}
}

func validFacts() hostcheck.Facts {
	return hostcheck.Facts{
		OperatingSystem:   "linux",
		Architecture:      "amd64",
		DistributionID:    "debian",
		DistributionMajor: 13,
		EffectiveUID:      0,
		SystemdVersion:    257,
		CgroupV2:          true,
		Controllers:       []string{"cpu", "io", "memory", "pids"},
		OverlayFS:         true,
		NFTables:          true,
		ClockSynchronized: true,
		Port443Available:  true,
		FilesystemBytes:   40 << 30,
		FreeBytes:         30 << 30,
	}
}
