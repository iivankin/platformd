package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
)

type ConsolePassphraseServices interface {
	Stop(context.Context) error
	Start(context.Context) error
	Health(context.Context, string, string) error
}

type ConsolePassphraseResetter struct {
	Paths       layout.Paths
	ExpectedUID int
	Random      io.Reader
	Now         func() time.Time
	Provide     func() ([]byte, error)
	AcquireLock func(string, int) (io.Closer, error)
	Services    ConsolePassphraseServices
}

func ProductionConsolePassphraseResetter(provide func() ([]byte, error)) ConsolePassphraseResetter {
	return ConsolePassphraseResetter{
		Paths: layout.Production(), ExpectedUID: 0, Random: rand.Reader, Now: time.Now,
		Provide: provide,
		AcquireLock: func(path string, expectedUID int) (io.Closer, error) {
			return singletonlock.Acquire(path, expectedUID)
		},
		Services: SystemdManager{},
	}
}

func (resetter ConsolePassphraseResetter) Run(ctx context.Context) (returnErr error) {
	if resetter.Paths.StateDatabase == "" || resetter.Paths.DaemonLock == "" || resetter.ExpectedUID < 0 ||
		resetter.Random == nil || resetter.Now == nil || resetter.Provide == nil ||
		resetter.AcquireLock == nil || resetter.Services == nil {
		return errors.New("console passphrase resetter configuration is incomplete")
	}
	if os.Geteuid() != resetter.ExpectedUID {
		return errors.New("console passphrase reset must run as the installation owner")
	}
	info, err := os.Lstat(resetter.Paths.StateDatabase)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("console passphrase reset requires an existing installation")
	}
	value, err := resetter.Provide()
	if err != nil {
		return err
	}
	defer clear(value)
	verifier, err := passphrase.HashWith(value, resetter.Random)
	if err != nil {
		return err
	}
	timestamp := resetter.Now()
	auditID, err := id.NewWith(timestamp, resetter.Random)
	if err != nil {
		return err
	}
	if err := resetter.Services.Stop(ctx); err != nil {
		return fmt.Errorf("stop platformd for console passphrase reset: %w", err)
	}
	restartNeeded := true
	defer func() {
		if !restartNeeded {
			return
		}
		restartContext, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		returnErr = errors.Join(returnErr, resetter.Services.Start(restartContext))
	}()

	lock, err := resetter.AcquireLock(resetter.Paths.DaemonLock, resetter.ExpectedUID)
	if err != nil {
		return fmt.Errorf("acquire daemon lock for console passphrase reset: %w", err)
	}
	lockOpen := true
	defer func() {
		if lockOpen {
			returnErr = errors.Join(returnErr, lock.Close())
		}
	}()

	store, err := state.Open(ctx, resetter.Paths.StateDatabase, resetter.ExpectedUID)
	if err != nil {
		return err
	}
	installation, stateErr := store.Installation(ctx)
	var certificatePEM string
	if stateErr == nil {
		certificatePEM, stateErr = CertificateForHostname(installation.AdminHostname, installation.OriginCertificates)
	}
	if stateErr == nil {
		stateErr = store.ResetConsolePassphrase(ctx, state.ResetConsolePassphrase{
			Verifier: verifier, AuditEventID: auditID, ResetAtMillis: timestamp.UnixMilli(),
		})
	}
	closeErr := store.Close()
	if stateErr != nil || closeErr != nil {
		return errors.Join(stateErr, closeErr)
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lockOpen = false
	if err := resetter.Services.Start(ctx); err != nil {
		return fmt.Errorf("restart platformd after console passphrase reset: %w", err)
	}
	restartNeeded = false
	return resetter.Services.Health(ctx, installation.AdminHostname, certificatePEM)
}
