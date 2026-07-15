package cgroupstats

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMountRoot = "/sys/fs/cgroup"
	procMemoryInfo   = "/proc/meminfo"
)

type Kind string

var ErrInvalidResource = errors.New("invalid cgroup resource")

const (
	Service  Kind = "service"
	Postgres Kind = "postgres"
	Redis    Kind = "redis"
)

type Sample struct {
	ObservedAtMillis int64
	CPUUsageMicros   uint64
	MemoryBytes      uint64
	HostCPUCores     int
	HostMemoryBytes  uint64
	Running          bool
}

type Capacity func() (cpuCores int, memoryBytes uint64, err error)

type Config struct {
	MountRoot    string
	WorkloadPath string
	Capacity     Capacity
	Now          func() time.Time
}

type Reader struct {
	root     string
	capacity Capacity
	now      func() time.Time
}

func NewProduction(workloadPath string) (*Reader, error) {
	return New(Config{
		MountRoot: defaultMountRoot, WorkloadPath: workloadPath,
		Capacity: hostCapacity, Now: time.Now,
	})
}

func New(config Config) (*Reader, error) {
	if !filepath.IsAbs(config.MountRoot) || filepath.Clean(config.MountRoot) != config.MountRoot ||
		config.MountRoot == string(filepath.Separator) || !path.IsAbs(config.WorkloadPath) ||
		path.Clean(config.WorkloadPath) != config.WorkloadPath || config.WorkloadPath == "/" || config.Capacity == nil {
		return nil, errors.New("cgroup stats roots and capacity source are invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	root := filepath.Join(config.MountRoot, filepath.FromSlash(strings.TrimPrefix(config.WorkloadPath, "/")))
	return &Reader{root: root, capacity: config.Capacity, now: config.Now}, nil
}

func (reader *Reader) Read(kind Kind, resourceID string) (Sample, error) {
	component, err := resourceComponent(kind, resourceID)
	if err != nil {
		return Sample{}, err
	}
	cpuCores, hostMemory, err := reader.capacity()
	if err != nil || cpuCores < 1 || hostMemory == 0 {
		return Sample{}, errors.Join(err, errors.New("host CPU or memory capacity is unavailable"))
	}
	sample := Sample{
		ObservedAtMillis: reader.now().UnixMilli(), HostCPUCores: cpuCores, HostMemoryBytes: hostMemory,
	}
	resourceRoot := filepath.Join(reader.root, component)
	populated, err := namedValue(filepath.Join(resourceRoot, "cgroup.events"), "populated")
	if errors.Is(err, os.ErrNotExist) {
		return sample, nil
	}
	if err != nil {
		return Sample{}, fmt.Errorf("read resource cgroup events: %w", err)
	}
	if populated != "1" {
		if populated == "0" {
			return sample, nil
		}
		return Sample{}, errors.New("resource cgroup populated flag is invalid")
	}
	cpuValue, err := namedValue(filepath.Join(resourceRoot, "cpu.stat"), "usage_usec")
	if err != nil {
		return Sample{}, fmt.Errorf("read resource CPU usage: %w", err)
	}
	sample.CPUUsageMicros, err = strconv.ParseUint(cpuValue, 10, 64)
	if err != nil {
		return Sample{}, errors.New("resource CPU usage is invalid")
	}
	memoryValue, err := os.ReadFile(filepath.Join(resourceRoot, "memory.current"))
	if err != nil {
		return Sample{}, fmt.Errorf("read resource memory usage: %w", err)
	}
	sample.MemoryBytes, err = strconv.ParseUint(strings.TrimSpace(string(memoryValue)), 10, 64)
	if err != nil {
		return Sample{}, errors.New("resource memory usage is invalid")
	}
	sample.Running = true
	return sample, nil
}

func resourceComponent(kind Kind, resourceID string) (string, error) {
	if !validResourceID(resourceID) {
		return "", fmt.Errorf("%w: ID", ErrInvalidResource)
	}
	switch kind {
	case Service, Postgres, Redis:
		return string(kind) + "-" + resourceID, nil
	default:
		return "", fmt.Errorf("%w: kind", ErrInvalidResource)
	}
}

func ValidateResource(kind Kind, resourceID string) error {
	_, err := resourceComponent(kind, resourceID)
	return err
}

func validResourceID(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_.", character) {
			continue
		}
		return false
	}
	return true
}

func namedValue(filename, name string) (string, error) {
	value, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(value)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("cgroup field %q is missing", name)
}

func hostCapacity() (int, uint64, error) {
	file, err := os.Open(procMemoryInfo)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr != nil || kilobytes > ^uint64(0)/1024 {
				return 0, 0, errors.New("host MemTotal is invalid")
			}
			return runtime.NumCPU(), kilobytes * 1024, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, errors.New("host MemTotal is missing")
}
