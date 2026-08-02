//go:build linux

package hostmetrics

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	procStat       = "/proc/stat"
	procMemoryInfo = "/proc/meminfo"
	procNetDev     = "/proc/net/dev"
	procIPv4Routes = "/proc/net/route"
	procIPv6Routes = "/proc/net/ipv6_route"
)

type productionReader struct {
	interfaceName string
	cpuCores      int
	now           func() time.Time
}

func NewProduction() (Reader, error) {
	interfaceName, err := defaultNetworkInterface()
	if err != nil {
		return nil, err
	}
	return &productionReader{interfaceName: interfaceName, cpuCores: runtime.NumCPU(), now: time.Now}, nil
}

func (reader *productionReader) Read() (Sample, error) {
	cpu, idle, err := readCPUUnits(procStat)
	if err != nil {
		return Sample{}, err
	}
	memoryUsed, memoryTotal, err := readMemory(procMemoryInfo)
	if err != nil {
		return Sample{}, err
	}
	rx, tx, err := readNetwork(procNetDev, reader.interfaceName)
	if err != nil {
		return Sample{}, err
	}
	return Sample{
		ObservedAtMillis: reader.now().UnixMilli(), CPUUnits: cpu, CPUIdleUnits: idle,
		CPUCores: reader.cpuCores, MemoryUsedBytes: memoryUsed, MemoryTotalBytes: memoryTotal,
		NetworkRXBytes: rx, NetworkTXBytes: tx, NetworkInterface: reader.interfaceName,
	}, nil
}

func readCPUUnits(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open host CPU counters: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, errors.New("host CPU counters are empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 6 || fields[0] != "cpu" {
		return 0, 0, errors.New("host CPU counters are invalid")
	}
	values := make([]uint64, 0, len(fields)-1)
	var total uint64
	for index, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return 0, 0, errors.New("host CPU counters are invalid")
		}
		values = append(values, value)
		// guest and guest_nice are already included in user and nice.
		if index < 8 {
			if total > ^uint64(0)-value {
				return 0, 0, errors.New("host CPU counters are invalid")
			}
			total += value
		}
	}
	return total, values[3] + values[4], nil
}

func readMemory(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open host memory counters: %w", err)
	}
	defer file.Close()
	var totalKiB, availableKiB uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, 0, errors.New("host memory counters are invalid")
		}
		switch fields[0] {
		case "MemTotal:":
			totalKiB = value
		case "MemAvailable:":
			availableKiB = value
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("read host memory counters: %w", err)
	}
	if totalKiB == 0 || availableKiB > totalKiB {
		return 0, 0, errors.New("host memory counters are incomplete")
	}
	return (totalKiB - availableKiB) * 1024, totalKiB * 1024, nil
}

func readNetwork(path, interfaceName string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open host network counters: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		name, counters, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != interfaceName {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) < 16 {
			return 0, 0, errors.New("host network counters are invalid")
		}
		rx, rxErr := strconv.ParseUint(fields[0], 10, 64)
		tx, txErr := strconv.ParseUint(fields[8], 10, 64)
		if rxErr != nil || txErr != nil {
			return 0, 0, errors.New("host network counters are invalid")
		}
		return rx, tx, nil
	}
	return 0, 0, fmt.Errorf("external network interface %q is missing", interfaceName)
}

func defaultNetworkInterface() (string, error) {
	if name := defaultIPv4Interface(procIPv4Routes); name != "" {
		return name, nil
	}
	if name := defaultIPv6Interface(procIPv6Routes); name != "" {
		return name, nil
	}
	return "", errors.New("default network interface is unavailable")
}

func defaultIPv4Interface(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	var selected string
	selectedMetric := ^uint64(0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[1] != "00000000" {
			continue
		}
		flags, flagsErr := strconv.ParseUint(fields[3], 16, 64)
		metric, metricErr := strconv.ParseUint(fields[6], 10, 64)
		if flagsErr == nil && metricErr == nil && flags&1 != 0 && metric < selectedMetric {
			selected, selectedMetric = fields[0], metric
		}
	}
	return selected
}

func defaultIPv6Interface(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	var selected string
	selectedMetric := ^uint64(0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[0] != strings.Repeat("0", 32) || fields[1] != "00" {
			continue
		}
		metric, metricErr := strconv.ParseUint(fields[5], 16, 64)
		if metricErr == nil && metric < selectedMetric {
			selected, selectedMetric = fields[9], metric
		}
	}
	return selected
}
