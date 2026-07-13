package disasterrestore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/hostcheck"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
)

type RestoreRemote interface {
	backup.ControlRemote
	Probe(context.Context) error
}

type RemoteFactory func(remotes3.Config) (RestoreRemote, error)

type Restorer struct {
	Paths         layout.Paths
	ExpectedUID   int
	PublicKey     ed25519.PublicKey
	ProvideInput  func() (ValidatedInput, error)
	ValidateHost  func(context.Context, string) error
	RemoteFactory RemoteFactory
	ImportExact   ExactImporter
	AcquireLock   func(string, int) (io.Closer, error)
	Services      bootstrap.ServiceManager
	Now           func() time.Time
	Random        io.Reader
	OS            string
	Architecture  string
}

func ProductionRestorer(provider func() (ValidatedInput, error)) (Restorer, error) {
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return Restorer{}, err
	}
	return Restorer{
		Paths: layout.Production(), ExpectedUID: 0, PublicKey: publicKey, ProvideInput: provider,
		ValidateHost: func(ctx context.Context, path string) error {
			facts, err := hostcheck.Collect(ctx, path)
			if err != nil {
				return err
			}
			return facts.Validate()
		},
		RemoteFactory: func(config remotes3.Config) (RestoreRemote, error) { return remotes3.New(config) },
		ImportExact:   RunExactImporter,
		AcquireLock: func(path string, expectedUID int) (io.Closer, error) {
			return singletonlock.Acquire(path, expectedUID)
		},
		Services: bootstrap.SystemdManager{}, Now: time.Now, Random: rand.Reader,
		OS: runtime.GOOS, Architecture: runtime.GOARCH,
	}, nil
}

func (restorer Restorer) Restore(ctx context.Context) error {
	if err := restorer.validate(); err != nil {
		return err
	}
	if os.Geteuid() != restorer.ExpectedUID {
		return errors.New("disaster restore must run as the installation owner")
	}
	if _, err := os.Lstat(restorer.Paths.StateDatabase); err == nil {
		return errors.New("disaster restore requires an empty installation without SQLite state")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock, err := restorer.AcquireLock(restorer.Paths.DaemonLock, restorer.ExpectedUID)
	if err != nil {
		return fmt.Errorf("require stopped platformd service: %w", err)
	}
	lockOpen := true
	defer func() {
		if lockOpen {
			_ = lock.Close()
		}
	}()
	preflightParent := nearestExistingParent(restorer.Paths.DataRoot)
	if err := restorer.ValidateHost(ctx, preflightParent); err != nil {
		return err
	}
	input, err := restorer.ProvideInput()
	if err != nil {
		return err
	}
	defer clear(input.Master[:])
	remote, err := restorer.RemoteFactory(input.Remote)
	if err != nil {
		return err
	}
	if err := remote.Probe(ctx); err != nil {
		return fmt.Errorf("probe restore target: %w", err)
	}
	// Fetch into an ephemeral directory beside the future data root. This keeps
	// cross-architecture/signature failures out of persistent product state and
	// still guarantees that the final SQLite hard link stays on one filesystem.
	preflightRoot, err := os.MkdirTemp(preflightParent, ".platformd-restore-")
	if err != nil {
		return fmt.Errorf("create restore preflight directory: %w", err)
	}
	defer os.RemoveAll(preflightRoot)
	if err := ensurePrivateRestoreDirectory(preflightRoot, restorer.ExpectedUID); err != nil {
		return err
	}
	fetched, err := backup.FetchControl(ctx, backup.ControlFetchConfig{
		Remote: remote, Master: input.Master, WorkRoot: preflightRoot,
		ExpectedUID: restorer.ExpectedUID, PublicKey: restorer.PublicKey,
		OS: restorer.OS, Architecture: restorer.Architecture,
	})
	if err != nil {
		return err
	}
	defer os.RemoveAll(fetched.WorkDirectory)
	if err := ensurePrivateRestoreDirectory(restorer.Paths.DataRoot, restorer.ExpectedUID); err != nil {
		return err
	}
	if err := ensurePrivateRestoreDirectory(restorer.Paths.ConfigRoot, restorer.ExpectedUID); err != nil {
		return err
	}
	// A fresh restore has no concurrent backup work. Removing the entire local
	// work root makes a power-loss retry independent of stale staging files.
	if err := os.RemoveAll(restorer.Paths.BackupWorkRoot); err != nil {
		return err
	}
	if err := ensurePrivateRestoreDirectory(restorer.Paths.BackupWorkRoot, restorer.ExpectedUID); err != nil {
		return err
	}
	if err := bootstrap.PublishReleaseSlot(fetched.Release, restorer.Paths, restorer.ExpectedUID); err != nil {
		return err
	}
	importedAt := restorer.Now().UnixMilli()
	payload, err := NewImportPayload(
		fetched.DatabasePath, fetched.Manifest, input, importedAt, restorer.ExpectedUID, restorer.Random,
	)
	if err != nil {
		return err
	}
	result, err := restorer.ImportExact(ctx, fetched.ReleaseBinaryPath, payload)
	if err != nil {
		return err
	}
	inspection, err := state.InspectDatabase(ctx, fetched.DatabasePath, restorer.ExpectedUID, true)
	if err != nil || inspection.SchemaVersion != fetched.Manifest.SchemaVersion {
		return errors.Join(err, errors.New("exact restore importer changed SQLite schema unexpectedly"))
	}
	if err := masterkey.Install(restorer.Paths.MasterKey, restorer.ExpectedUID, input.Master); err != nil {
		return err
	}
	if err := bootstrap.SwitchCurrentRelease(restorer.Paths, fetched.Release.Manifest.Version); err != nil {
		return err
	}
	if err := bootstrap.InstallEntrypoints(restorer.Paths, restorer.ExpectedUID); err != nil {
		return err
	}
	// Publish SQLite only after the public CLI symlink points at the saved exact
	// release. If power is lost after this boundary, ordinary `platformd init`
	// therefore repairs with the schema-compatible binary.
	if err := publishRestoredDatabase(ctx, fetched.DatabasePath, restorer.Paths.StateDatabase, restorer.ExpectedUID); err != nil {
		return err
	}
	if err := restorer.Services.ReloadAndEnable(ctx); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lockOpen = false
	if err := restorer.Services.Start(ctx); err != nil {
		return err
	}
	return restorer.Services.Health(ctx, result.AdminHostname, result.OriginCertificatePEM)
}

func (restorer Restorer) validate() error {
	if restorer.Paths.DataRoot == "" || restorer.Paths.ConfigRoot == "" || restorer.Paths.StateDatabase == "" ||
		restorer.Paths.MasterKey == "" || restorer.Paths.BackupWorkRoot == "" || restorer.ExpectedUID < 0 ||
		len(restorer.PublicKey) != ed25519.PublicKeySize || restorer.ProvideInput == nil || restorer.ValidateHost == nil ||
		restorer.RemoteFactory == nil || restorer.ImportExact == nil || restorer.AcquireLock == nil ||
		restorer.Services == nil || restorer.Now == nil || restorer.Random == nil || restorer.OS == "" || restorer.Architecture == "" {
		return errors.New("disaster restorer configuration is incomplete")
	}
	if !restoreSubpath(restorer.Paths.DataRoot, restorer.Paths.StateDatabase) ||
		!restoreSubpath(restorer.Paths.DataRoot, restorer.Paths.BackupWorkRoot) ||
		!restoreSubpath(restorer.Paths.DataRoot, restorer.Paths.ReleasesRoot) {
		return errors.New("disaster restore data paths escape the data root")
	}
	return nil
}

func publishRestoredDatabase(ctx context.Context, source, destination string, expectedUID int) error {
	if err := ensurePrivateRestoreDirectory(filepath.Dir(destination), expectedUID); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("restored SQLite source is unsafe")
	}
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("publish restored SQLite: %w", err)
	}
	if err := syncRestoreDirectory(filepath.Dir(destination)); err != nil {
		_ = os.Remove(destination)
		return err
	}
	if _, err := state.InspectDatabase(ctx, destination, expectedUID, true); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

func restoreSubpath(root, path string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensurePrivateRestoreDirectory(path string, expectedUID int) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("restore directory %s is unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != expectedUID {
		return fmt.Errorf("restore directory %s ownership is unsafe", path)
	}
	return nil
}

func syncRestoreDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func nearestExistingParent(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}
