package bootstrap_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/state"
)

type consolePassphraseServicesStub struct {
	events         *[]string
	healthHostname string
}

func (services *consolePassphraseServicesStub) Stop(context.Context) error {
	*services.events = append(*services.events, "stop")
	return nil
}

func (services *consolePassphraseServicesStub) Start(context.Context) error {
	*services.events = append(*services.events, "start")
	return nil
}

func (services *consolePassphraseServicesStub) Health(_ context.Context, hostname, certificate string) error {
	if certificate == "" {
		return errors.New("missing health certificate")
	}
	services.healthHostname = hostname
	*services.events = append(*services.events, "health")
	return nil
}

type eventCloser struct {
	events *[]string
}

func (closer eventCloser) Close() error {
	*closer.events = append(*closer.events, "unlock")
	return nil
}

func TestConsolePassphraseResetStopsDaemonAndChangesOnlyVerifier(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths := layout.FromRoots(
		filepath.Join(root, "data"),
		filepath.Join(root, "config"),
		filepath.Join(root, "run"),
		filepath.Join(root, "bin", "platformd"),
		filepath.Join(root, "systemd", "platformd.service"),
	)
	ctx := context.Background()
	store, err := state.Open(ctx, paths.StateDatabase, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	oldVerifier, err := passphrase.Hash([]byte("old passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := testCertificate(t, []string{"admin.example.com"})
	createdAt := time.Unix(1_800_000_000, 0).UnixMilli()
	if err := store.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: oldVerifier, OriginCertificateID: "certificate",
		OriginCertificatePEM: certificate, OriginPrivateKey: []byte("encrypted"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	events := []string{}
	services := &consolePassphraseServicesStub{events: &events}
	resetter := bootstrap.ConsolePassphraseResetter{
		Paths: paths, ExpectedUID: os.Geteuid(), Random: rand.Reader,
		Now:     func() time.Time { return time.Unix(1_800_000_100, 0) },
		Provide: func() ([]byte, error) { return []byte("new passphrase"), nil },
		AcquireLock: func(path string, expectedUID int) (io.Closer, error) {
			if path != paths.DaemonLock || expectedUID != os.Geteuid() {
				t.Fatalf("lock arguments = %q/%d", path, expectedUID)
			}
			events = append(events, "lock")
			return eventCloser{events: &events}, nil
		},
		Services: services,
	}
	if err := resetter.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"stop", "lock", "unlock", "start", "health"}) {
		t.Fatalf("events = %v", events)
	}
	if services.healthHostname != "admin.example.com" {
		t.Fatalf("health hostname = %q", services.healthHostname)
	}

	store, err = state.Open(ctx, paths.StateDatabase, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	installation, err := store.Installation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if installation.AdminHostname != "admin.example.com" || installation.CreatedAtMillis != createdAt {
		t.Fatalf("installation identity changed: %+v", installation)
	}
	verified, err := passphrase.Verify(installation.ConsolePassphrasePHC, []byte("new passphrase"))
	if err != nil || !verified {
		t.Fatalf("new passphrase verification = %v/%v", verified, err)
	}
	verified, err = passphrase.Verify(installation.ConsolePassphrasePHC, []byte("old passphrase"))
	if err != nil || verified {
		t.Fatalf("old passphrase verification = %v/%v", verified, err)
	}
	var auditCount int
	if err := store.QueryRowContext(ctx, `
SELECT count(*) FROM audit_events
WHERE action = 'installation.console_passphrase_reset'
  AND actor_kind = 'local_root'
  AND actor_id = 'init'
  AND target_id = 'installation'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("reset audit count = %d", auditCount)
	}
}

func TestConsolePassphraseResetRestartsDaemonWhenLockCannotBeAcquired(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths := layout.FromRoots(
		filepath.Join(root, "data"),
		filepath.Join(root, "config"),
		filepath.Join(root, "run"),
		filepath.Join(root, "bin", "platformd"),
		filepath.Join(root, "systemd", "platformd.service"),
	)
	if err := os.MkdirAll(filepath.Dir(paths.StateDatabase), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StateDatabase, []byte("existing state"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	services := &consolePassphraseServicesStub{events: &events}
	resetter := bootstrap.ConsolePassphraseResetter{
		Paths: paths, ExpectedUID: os.Geteuid(), Random: rand.Reader,
		Now:     func() time.Time { return time.Unix(1_800_000_100, 0) },
		Provide: func() ([]byte, error) { return []byte("new passphrase"), nil },
		AcquireLock: func(string, int) (io.Closer, error) {
			events = append(events, "lock-failed")
			return nil, errors.New("lock unavailable")
		},
		Services: services,
	}
	if err := resetter.Run(context.Background()); err == nil {
		t.Fatal("lock failure was ignored")
	}
	if !slices.Equal(events, []string{"stop", "lock-failed", "start"}) {
		t.Fatalf("events = %v", events)
	}
}
