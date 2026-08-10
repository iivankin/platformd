package errortracker

import (
	"context"
	"errors"
	"fmt"
	"net"
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

type ProcessConfig struct {
	Binary      string
	Volume      string
	RuntimeRoot string
	TrackerID   string
	Slug        string
	PublicURL   string
}

type Process struct {
	command  *exec.Cmd
	done     chan struct{}
	close    sync.Once
	waitMu   sync.Mutex
	waitErr  error
	closeErr error
	target   *url.URL
}

func StartProcess(ctx context.Context, config ProcessConfig) (*Process, error) {
	if ctx == nil || config.Binary == "" || config.Volume == "" || config.RuntimeRoot == "" ||
		config.TrackerID == "" || config.Slug == "" || config.PublicURL == "" {
		return nil, errors.New("error tracker process configuration is incomplete")
	}
	if filepath.Base(config.TrackerID) != config.TrackerID {
		return nil, errors.New("error tracker runtime identity is invalid")
	}
	if err := os.MkdirAll(config.RuntimeRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create error tracker runtime directory: %w", err)
	}
	endpointFile := filepath.Join(config.RuntimeRoot, config.TrackerID+".endpoint")
	if err := os.Remove(endpointFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale error tracker endpoint: %w", err)
	}
	command := exec.Command(config.Binary)
	command.Env = processEnvironment(os.Environ(), config, endpointFile)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start error tracker process: %w", err)
	}
	process := &Process{command: command, done: make(chan struct{})}
	go func() {
		err := command.Wait()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	target, err := waitForEndpoint(ctx, process, endpointFile)
	if err != nil {
		_ = process.Close()
		return nil, err
	}
	process.target = target
	return process, nil
}

func processEnvironment(base []string, config ProcessConfig, endpointFile string) []string {
	keys := map[string]struct{}{
		"ERROR_TRACKER_ADMIN_TOKEN":   {},
		"ERROR_TRACKER_ENDPOINT_FILE": {},
		"ERROR_TRACKER_LISTEN":        {},
		"ERROR_TRACKER_PUBLIC_URL":    {},
		"ERROR_TRACKER_SLUG":          {},
		"ERROR_TRACKER_UI_ENABLED":    {},
		"ERROR_TRACKER_VOLUME":        {},
	}
	environment := make([]string, 0, len(base)+6)
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		if _, managed := keys[name]; !managed {
			environment = append(environment, value)
		}
	}
	return append(environment,
		"ERROR_TRACKER_ENDPOINT_FILE="+endpointFile,
		"ERROR_TRACKER_LISTEN=127.0.0.1:0",
		"ERROR_TRACKER_PUBLIC_URL="+config.PublicURL,
		"ERROR_TRACKER_SLUG="+config.Slug,
		"ERROR_TRACKER_UI_ENABLED=false",
		"ERROR_TRACKER_VOLUME="+config.Volume,
	)
}

func waitForEndpoint(ctx context.Context, process *Process, endpointFile string) (*url.URL, error) {
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		value, err := os.ReadFile(endpointFile)
		if err == nil {
			address := strings.TrimSpace(string(value))
			host, port, splitErr := net.SplitHostPort(address)
			ip := net.ParseIP(host)
			if splitErr == nil && ip != nil && ip.IsLoopback() && port != "0" {
				target := &url.URL{Scheme: "http", Host: address}
				healthContext, cancel := context.WithTimeout(ctx, time.Second)
				healthErr := health(healthContext, target)
				cancel()
				if healthErr == nil {
					return target, nil
				}
				lastErr = healthErr
			} else {
				lastErr = errors.New("error tracker published an invalid loopback endpoint")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			lastErr = err
		}
		select {
		case <-process.done:
			return nil, fmt.Errorf("error tracker exited during startup: %w", process.WaitError())
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			if lastErr == nil {
				lastErr = errors.New("endpoint file was not published")
			}
			return nil, fmt.Errorf("error tracker startup timed out: %w", lastErr)
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
		return fmt.Errorf("error tracker health returned HTTP %d", response.StatusCode)
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
			result = errors.Join(errors.New("error tracker shutdown timed out"), process.command.Process.Kill())
		}
	})
	process.waitMu.Lock()
	defer process.waitMu.Unlock()
	return process.closeErr
}
