//go:build linux && amd64 && cgo

package containerengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containers/podman/v5/libpod/define"
)

func (e *Engine) ContainerListeningPorts(id string) ([]ListeningPort, error) {
	container, err := e.lookupContainer(id)
	if err != nil {
		return nil, err
	}
	state, err := container.State()
	if err != nil {
		return nil, fmt.Errorf("read container %s state: %w", id, err)
	}
	if state != define.ContainerStateRunning {
		return nil, fmt.Errorf("container %s is not running", id)
	}
	pid, err := container.PID()
	if err != nil {
		return nil, fmt.Errorf("read container %s process ID: %w", id, err)
	}
	if pid < 1 {
		return nil, fmt.Errorf("container %s process ID is unavailable", id)
	}

	tables := []struct {
		name string
		procSocketTable
	}{
		{name: "tcp", procSocketTable: procSocketTable{protocol: "tcp", state: "0A"}},
		{name: "tcp6", procSocketTable: procSocketTable{protocol: "tcp", state: "0A"}},
		{name: "udp", procSocketTable: procSocketTable{protocol: "udp", state: "07"}},
		{name: "udp6", procSocketTable: procSocketTable{protocol: "udp", state: "07"}},
	}
	ports := make([]ListeningPort, 0)
	readTables := 0
	for _, table := range tables {
		file, openErr := os.Open(filepath.Join("/proc", fmt.Sprint(pid), "net", table.name))
		if errors.Is(openErr, os.ErrNotExist) {
			continue
		}
		if openErr != nil {
			return nil, fmt.Errorf("open container %s %s socket table: %w", id, table.name, openErr)
		}
		readTables++
		detected, parseErr := parseProcListeningPorts(file, table.procSocketTable)
		closeErr := file.Close()
		if parseErr != nil {
			return nil, fmt.Errorf("read container %s %s socket table: %w", id, table.name, parseErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close container %s %s socket table: %w", id, table.name, closeErr)
		}
		ports = append(ports, detected...)
	}
	if readTables == 0 {
		return nil, fmt.Errorf("container %s network socket tables are unavailable", id)
	}
	return uniqueListeningPorts(ports), nil
}
