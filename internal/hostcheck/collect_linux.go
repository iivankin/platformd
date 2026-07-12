//go:build linux

package hostcheck

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const probeTimeout = 5 * time.Second

func Collect(ctx context.Context, diskPath string) (Facts, error) {
	distributionID, distributionMajor, err := distribution()
	if err != nil {
		return Facts{}, err
	}
	systemdVersion, err := commandVersion(ctx, "systemctl", "--version")
	if err != nil {
		return Facts{}, err
	}
	controllers, cgroupV2, err := cgroupControllers()
	if err != nil {
		return Facts{}, err
	}
	overlay, err := filesystemListed("overlay")
	if err != nil {
		return Facts{}, err
	}
	nftables := netfilterAvailable()
	clockSynchronized, err := synchronizedClock(ctx)
	if err != nil {
		return Facts{}, err
	}
	portAvailable := port443Available()
	filesystemBytes, freeBytes, err := diskCapacity(diskPath)
	if err != nil {
		return Facts{}, err
	}
	return Facts{
		OperatingSystem:   runtime.GOOS,
		Architecture:      runtime.GOARCH,
		DistributionID:    distributionID,
		DistributionMajor: distributionMajor,
		EffectiveUID:      os.Geteuid(),
		SystemdVersion:    systemdVersion,
		CgroupV2:          cgroupV2,
		Controllers:       controllers,
		OverlayFS:         overlay,
		NFTables:          nftables,
		ClockSynchronized: clockSynchronized,
		Port443Available:  portAvailable,
		FilesystemBytes:   filesystemBytes,
		FreeBytes:         freeBytes,
	}, nil
}

func distribution() (string, int, error) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", 0, fmt.Errorf("open os-release: %w", err)
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			values[key] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, fmt.Errorf("read os-release: %w", err)
	}
	majorText, _, _ := strings.Cut(values["VERSION_ID"], ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return "", 0, errors.New("os-release has invalid VERSION_ID")
	}
	return values["ID"], major, nil
}

func commandVersion(ctx context.Context, name string, arguments ...string) (int, error) {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := exec.CommandContext(probeContext, name, arguments...).Output()
	if err != nil {
		return 0, fmt.Errorf("run %s version probe: %w", name, err)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected %s version output", name)
	}
	version, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("parse %s version: %w", name, err)
	}
	return version, nil
}

func cgroupControllers() ([]string, bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/sys/fs/cgroup", &stat); err != nil {
		return nil, false, fmt.Errorf("inspect cgroup filesystem: %w", err)
	}
	if stat.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, false, nil
	}
	value, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		return nil, false, fmt.Errorf("read cgroup controllers: %w", err)
	}
	return strings.Fields(string(value)), true, nil
}

func filesystemListed(name string) (bool, error) {
	value, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false, fmt.Errorf("read filesystems: %w", err)
	}
	for line := range strings.Lines(string(value)) {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[len(fields)-1] == name {
			return true, nil
		}
	}
	return false, nil
}

func netfilterAvailable() bool {
	descriptor, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return false
	}
	_ = unix.Close(descriptor)
	return true
}

func synchronizedClock(ctx context.Context) (bool, error) {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := exec.CommandContext(probeContext, "timedatectl", "show", "--property=NTPSynchronized", "--value").Output()
	if err != nil {
		return false, fmt.Errorf("probe clock synchronization: %w", err)
	}
	return strings.TrimSpace(string(output)) == "yes", nil
}

func port443Available() bool {
	listener, err := net.Listen("tcp", ":443")
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func diskCapacity(path string) (uint64, uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("inspect installation filesystem: %w", err)
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}
