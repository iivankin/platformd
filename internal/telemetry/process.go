package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	OTLPGRPCAddress   = "127.0.0.1:4317"
	OTLPHTTPAddress   = "127.0.0.1:4318"
	SentryHTTPAddress = "127.0.0.1:4319"
)

type ProcessConfig struct {
	Binary string
	Volume string
}

type Process struct {
	command  *exec.Cmd
	client   *http.Client
	done     chan struct{}
	close    sync.Once
	waitMu   sync.Mutex
	waitErr  error
	closeErr error
	target   *url.URL
}

type backupView struct {
	ID string `json:"id"`
}

func StartProcess(ctx context.Context, config ProcessConfig) (*Process, error) {
	if ctx == nil || config.Binary == "" || config.Volume == "" {
		return nil, errors.New("telemetry process configuration is incomplete")
	}
	if err := os.MkdirAll(config.Volume, 0o700); err != nil {
		return nil, fmt.Errorf("create telemetry volume: %w", err)
	}
	command := exec.Command(config.Binary)
	command.Env = processEnvironment(os.Environ(), config)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start telemetry process: %w", err)
	}
	process := &Process{
		command: command, done: make(chan struct{}),
		client: &http.Client{Timeout: 15 * time.Second},
		target: &url.URL{Scheme: "http", Host: SentryHTTPAddress},
	}
	go func() {
		err := command.Wait()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	if err := waitForHealth(ctx, process); err != nil {
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func processEnvironment(base []string, config ProcessConfig) []string {
	managed := map[string]struct{}{
		"PLATFORMD_TELEMETRY_GEOIP_SOURCE":     {},
		"PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN": {},
		"PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN": {},
		"PLATFORMD_TELEMETRY_SENTRY_LISTEN":    {},
		"PLATFORMD_TELEMETRY_VOLUME":           {},
	}
	environment := make([]string, 0, len(base)+10)
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		if _, replaced := managed[name]; !replaced {
			environment = append(environment, value)
		}
	}
	return append(environment,
		"PLATFORMD_TELEMETRY_GEOIP_SOURCE=cloudflare",
		"PLATFORMD_TELEMETRY_SENTRY_LISTEN="+SentryHTTPAddress,
		"PLATFORMD_TELEMETRY_VOLUME="+config.Volume,
		"PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN="+OTLPGRPCAddress,
		"PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN="+OTLPHTTPAddress,
	)
}

func waitForHealth(ctx context.Context, process *Process) error {
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		healthContext, cancel := context.WithTimeout(ctx, time.Second)
		lastErr = health(healthContext, process.target)
		cancel()
		if lastErr == nil {
			return nil
		}
		select {
		case <-process.done:
			return fmt.Errorf("telemetry exited during startup: %w", process.WaitError())
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("telemetry startup timed out: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func health(ctx context.Context, target *url.URL) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String()+"/health", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (process *Process) Target() *url.URL {
	if process == nil || process.target == nil {
		return nil
	}
	copy := *process.target
	return &copy
}

func (process *Process) Snapshot(ctx context.Context, destination string) error {
	if process == nil || process.target == nil || ctx == nil || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errors.New("telemetry snapshot input is incomplete")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, process.target.String()+"/internal/backups", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("create telemetry snapshot: %w", err)
	}
	var snapshot backupView
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&snapshot)
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusCreated || decodeErr != nil || closeErr != nil || len(snapshot.ID) != 32 {
		return errors.Join(decodeErr, closeErr, fmt.Errorf("create telemetry snapshot returned HTTP %d", response.StatusCode))
	}
	defer process.deleteSnapshot(snapshot.ID)

	request, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		process.target.String()+"/internal/backups/"+url.PathEscape(snapshot.ID),
		nil,
	)
	if err != nil {
		return err
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("download telemetry snapshot: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download telemetry snapshot returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, response.Body)
	if copyErr == nil && written < 1 {
		copyErr = errors.New("telemetry snapshot is empty")
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	fileCloseErr := file.Close()
	if copyErr != nil || fileCloseErr != nil {
		_ = os.Remove(destination)
		return errors.Join(copyErr, fileCloseErr)
	}
	return nil
}

func (process *Process) deleteSnapshot(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		process.target.String()+"/internal/backups/"+url.PathEscape(id),
		nil,
	)
	if err != nil {
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
}

func (process *Process) Done() <-chan struct{} { return process.done }

func (process *Process) WaitError() error {
	process.waitMu.Lock()
	defer process.waitMu.Unlock()
	return process.waitErr
}

func (process *Process) Close() error {
	process.close.Do(func() {
		var result error
		defer func() {
			process.waitMu.Lock()
			process.closeErr = result
			process.waitMu.Unlock()
		}()
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
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-process.done:
			err := process.WaitError()
			var exit *exec.ExitError
			if err != nil && (!errors.As(err, &exit) || !exit.Exited()) {
				result = err
			}
		case <-timer.C:
			result = errors.Join(errors.New("telemetry shutdown timed out"), process.command.Process.Kill())
		}
	})
	process.waitMu.Lock()
	defer process.waitMu.Unlock()
	return process.closeErr
}
