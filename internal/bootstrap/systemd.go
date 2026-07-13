package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/iivankin/platformd/internal/layout"
)

const platformdUnit = `[Unit]
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=notify
ExecStart=/usr/local/bin/platformd __daemon
Restart=always
RestartSec=3
TimeoutStartSec=600
TimeoutStopSec=120
LimitNOFILE=1048576
TasksMax=infinity
Delegate=yes
DelegateSubgroup=control
KillMode=mixed
UMask=0022

[Install]
WantedBy=multi-user.target
`

type SystemdManager struct{}

func (SystemdManager) ReloadAndEnable(ctx context.Context) error {
	if err := runSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	return runSystemctl(ctx, "enable", "platformd.service")
}

func (SystemdManager) Start(ctx context.Context) error {
	return runSystemctl(ctx, "start", "platformd.service")
}

func (SystemdManager) Stop(ctx context.Context) error {
	return runSystemctl(ctx, "stop", "platformd.service")
}

func (SystemdManager) Health(ctx context.Context, hostname, certificatePEM string) error {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		return errors.New("Origin certificate PEM has no certificate")
	}
	expected, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse expected Origin certificate: %w", err)
	}
	expectedHash := sha256.Sum256(expected.Raw)
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // VerifyConnection pins the configured leaf and hostname below.
		ServerName:         hostname,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("local TLS health response has no peer certificate")
			}
			if err := state.PeerCertificates[0].VerifyHostname(hostname); err != nil {
				return err
			}
			actualHash := sha256.Sum256(state.PeerCertificates[0].Raw)
			if !bytes.Equal(actualHash[:], expectedHash[:]) {
				return errors.New("local TLS health response used an unexpected certificate")
			}
			return nil
		},
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", "127.0.0.1:443")
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastError error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostname+"/healthz", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
			err = fmt.Errorf("local health status %d: %w", response.StatusCode, errors.Join(readErr, closeErr))
		}
		lastError = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("local HTTPS readiness timed out: %w", lastError)
		case <-ticker.C:
		}
	}
}

func installSystemdUnit(paths layout.Paths, expectedUID int) error {
	if err := writeAtomicFile(paths.UnitFile, []byte(platformdUnit), 0o644); err != nil {
		return fmt.Errorf("install systemd unit: %w", err)
	}
	info, err := os.Lstat(paths.UnitFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		return errors.New("installed systemd unit is unsafe")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != expectedUID {
		return errors.New("installed systemd unit ownership is unsafe")
	}
	return nil
}

func writeAtomicFile(path string, value []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".platformd-file-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(value)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(writeErr, syncErr, closeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func runSystemctl(ctx context.Context, arguments ...string) error {
	command := exec.CommandContext(ctx, "systemctl", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %v: %w: %s", arguments, err, bytes.TrimSpace(output))
	}
	return nil
}
