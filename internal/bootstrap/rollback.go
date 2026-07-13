package bootstrap

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
)

type UpdateRollback struct {
	Paths         layout.Paths
	ExpectedUID   int
	PublicKey     ed25519.PublicKey
	SchemaVersion int
	Executable    string
	AcquireLock   func(string, int) (io.Closer, error)
	StartService  func(context.Context) error
}

func ProductionUpdateRollback() (UpdateRollback, error) {
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return UpdateRollback{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return UpdateRollback{}, err
	}
	services := SystemdManager{}
	return UpdateRollback{
		Paths: layout.Production(), ExpectedUID: 0, PublicKey: publicKey,
		SchemaVersion: state.SupportedSchemaVersion(), Executable: executable,
		AcquireLock: func(path string, expectedUID int) (io.Closer, error) {
			return singletonlock.Acquire(path, expectedUID)
		},
		StartService: services.Start,
	}, nil
}

func (rollback UpdateRollback) Run(ctx context.Context) error {
	if rollback.Paths.ReleasesRoot == "" || rollback.Paths.Current == "" || rollback.Paths.Previous == "" ||
		rollback.Paths.StateDatabase == "" || rollback.Paths.DaemonLock == "" || rollback.ExpectedUID < 0 ||
		len(rollback.PublicKey) != ed25519.PublicKeySize || rollback.SchemaVersion < 1 || rollback.Executable == "" ||
		rollback.AcquireLock == nil || rollback.StartService == nil {
		return errors.New("update rollback configuration is incomplete")
	}
	if os.Geteuid() != rollback.ExpectedUID {
		return errors.New("update rollback must run as the installation owner")
	}
	lock, err := rollback.AcquireLock(rollback.Paths.DaemonLock, rollback.ExpectedUID)
	if err != nil {
		return fmt.Errorf("require stopped platformd unit: %w", err)
	}
	lockOpen := true
	defer func() {
		if lockOpen {
			_ = lock.Close()
		}
	}()

	current, err := CurrentReleaseManifest(rollback.Paths, rollback.PublicKey, rollback.ExpectedUID)
	if err != nil {
		return err
	}
	previousName, err := os.Readlink(rollback.Paths.Previous)
	if err != nil || !validReleaseName(previousName) || previousName == current.Version {
		return errors.New("previous release link is unavailable or invalid")
	}
	previousSlot := filepath.Join(rollback.Paths.ReleasesRoot, previousName)
	if err := VerifyReleaseSlot(previousSlot, nil, rollback.PublicKey, rollback.ExpectedUID); err != nil {
		return fmt.Errorf("verify previous release: %w", err)
	}
	executable, err := filepath.EvalSymlinks(rollback.Executable)
	if err != nil {
		return err
	}
	expectedExecutable, err := filepath.EvalSymlinks(filepath.Join(previousSlot, "platformd"))
	if err != nil || executable != expectedExecutable {
		return errors.New("rollback command must be executed by the saved previous binary")
	}
	version, err := state.ReadSchemaVersion(ctx, rollback.Paths.StateDatabase, rollback.ExpectedUID)
	if err != nil {
		return err
	}
	if version != rollback.SchemaVersion {
		return fmt.Errorf("SQLite schema version = %d, previous binary requires %d; rollback is forbidden", version, rollback.SchemaVersion)
	}
	if err := SwitchCurrentRelease(rollback.Paths, previousName); err != nil {
		return err
	}
	if err := os.Remove(rollback.Paths.Previous); err != nil {
		return err
	}
	if err := syncDirectory(rollback.Paths.ReleasesRoot); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lockOpen = false
	return rollback.StartService(ctx)
}
