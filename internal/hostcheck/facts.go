package hostcheck

import (
	"errors"
	"fmt"
	"slices"
)

type Facts struct {
	OperatingSystem   string
	Architecture      string
	DistributionID    string
	DistributionMajor int
	EffectiveUID      int
	SystemdVersion    int
	CgroupV2          bool
	Controllers       []string
	OverlayFS         bool
	NFTables          bool
	ClockSynchronized bool
	Port443Available  bool
	FilesystemBytes   uint64
	FreeBytes         uint64
}

func (facts Facts) Validate() error {
	var failures []error
	if facts.EffectiveUID != 0 {
		failures = append(failures, errors.New("init must run as root"))
	}
	if facts.OperatingSystem != "linux" {
		failures = append(failures, fmt.Errorf("unsupported operating system %q", facts.OperatingSystem))
	}
	if facts.Architecture != "amd64" {
		failures = append(failures, fmt.Errorf("unsupported architecture %q; this release supports amd64", facts.Architecture))
	}
	if !supportedDistribution(facts.DistributionID, facts.DistributionMajor) {
		failures = append(failures, fmt.Errorf("unsupported distribution %s %d", facts.DistributionID, facts.DistributionMajor))
	}
	if facts.SystemdVersion < 255 {
		failures = append(failures, fmt.Errorf("systemd %d is older than required 255", facts.SystemdVersion))
	}
	if !facts.CgroupV2 {
		failures = append(failures, errors.New("cgroup v2 is unavailable"))
	}
	for _, controller := range []string{"cpu", "io", "memory", "pids"} {
		if !slices.Contains(facts.Controllers, controller) {
			failures = append(failures, fmt.Errorf("cgroup controller %q is unavailable", controller))
		}
	}
	if !facts.OverlayFS {
		failures = append(failures, errors.New("OverlayFS is unavailable"))
	}
	if !facts.NFTables {
		failures = append(failures, errors.New("nftables netlink is unavailable"))
	}
	if !facts.ClockSynchronized {
		failures = append(failures, errors.New("system clock is not synchronized"))
	}
	if !facts.Port443Available {
		failures = append(failures, errors.New("TCP port 443 is already in use"))
	}
	reserveBytes := max(uint64(1<<30), facts.FilesystemBytes/50)
	if facts.FreeBytes <= reserveBytes {
		failures = append(failures, fmt.Errorf("free disk space %d is insufficient for reserve %d", facts.FreeBytes, reserveBytes))
	}
	return errors.Join(failures...)
}

func supportedDistribution(id string, major int) bool {
	return (id == "debian" && major == 13) || (id == "ubuntu" && major == 24)
}
