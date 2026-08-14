package objectstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type SidecarProcess struct {
	command *exec.Cmd
	done    chan struct{}
	close   sync.Once
	waitMu  sync.Mutex
	waitErr error
}

func StartSidecar(ctx context.Context, binary, volume, socket string) (*SidecarProcess, *SidecarClient, error) {
	if ctx == nil || binary == "" || volume == "" || socket == "" {
		return nil, nil, errors.New("object store sidecar configuration is incomplete")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, nil, err
	}
	command := exec.Command(binary)
	command.Env = sidecarEnvironment(os.Environ(), volume, socket)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("start object store sidecar: %w", err)
	}
	process := &SidecarProcess{command: command, done: make(chan struct{})}
	go func() {
		err := command.Wait()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	client, err := NewSidecarClient(socket)
	if err != nil {
		_ = process.Close()
		return nil, nil, err
	}
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		healthContext, cancel := context.WithTimeout(ctx, time.Second)
		healthErr := client.Health(healthContext)
		cancel()
		if healthErr == nil {
			return process, client, nil
		}
		select {
		case <-process.done:
			return nil, nil, fmt.Errorf("object store sidecar exited during startup: %w", process.WaitError())
		case <-ctx.Done():
			_ = process.Close()
			return nil, nil, ctx.Err()
		case <-deadline.C:
			_ = process.Close()
			return nil, nil, fmt.Errorf("object store sidecar startup timed out: %w", healthErr)
		case <-ticker.C:
		}
	}
}

func sidecarEnvironment(base []string, volume, socket string) []string {
	const (
		volumeKey     = "PLATFORMD_OBJECTSTORE_VOLUME"
		socketKey     = "PLATFORMD_OBJECTSTORE_SOCKET"
		durabilityKey = "RUSTFS_DURABILITY_MODE"
		scannerSpeed  = "RUSTFS_SCANNER_SPEED"
		scannerCycle  = "RUSTFS_SCANNER_CYCLE"
	)
	environment := make([]string, 0, len(base)+4)
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		switch name {
		case volumeKey, socketKey, durabilityKey, scannerSpeed, scannerCycle:
			continue
		}
		environment = append(environment, value)
	}
	return append(environment,
		"PLATFORMD_OBJECTSTORE_VOLUME="+volume,
		"PLATFORMD_OBJECTSTORE_SOCKET="+socket,
		// Backups are the recovery boundary. Avoid synchronous object-data
		// durability while keeping RustFS system metadata pinned to strict.
		"RUSTFS_DURABILITY_MODE=none",
		// Keep usage/heal walks off the hot path: default ~1m cycles showed up as
		// regular host CPU spikes on quiet VPSes with modest object counts.
		"RUSTFS_SCANNER_SPEED=slow",
		"RUSTFS_SCANNER_CYCLE=1800",
	)
}

func (process *SidecarProcess) Done() <-chan struct{} { return process.done }

func (process *SidecarProcess) WaitError() error {
	process.waitMu.Lock()
	defer process.waitMu.Unlock()
	return process.waitErr
}

func (process *SidecarProcess) Close() error {
	var result error
	process.close.Do(func() {
		select {
		case <-process.done:
			result = process.WaitError()
			return
		default:
		}
		if err := process.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = err
			return
		}
		timer := time.NewTimer(60 * time.Second)
		defer timer.Stop()
		select {
		case <-process.done:
			err := process.WaitError()
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || !exit.Exited() {
					result = err
				}
			}
		case <-timer.C:
			result = errors.Join(errors.New("object store sidecar shutdown timed out"), process.command.Process.Kill())
		}
	})
	return result
}
