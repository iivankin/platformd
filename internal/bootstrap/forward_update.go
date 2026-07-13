package bootstrap

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/singletonlock"
)

type SignedUpdateInstaller struct {
	Paths          layout.Paths
	ExpectedUID    int
	PublicKey      ed25519.PublicKey
	Executable     string
	ManifestSource string
	BinaryPath     string
	HTTPClient     *http.Client
	AcquireLock    func(string, int) (io.Closer, error)
	StartService   func(context.Context) error
}

func ProductionSignedUpdateInstaller(manifestSource, binaryPath string) (SignedUpdateInstaller, error) {
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return SignedUpdateInstaller{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return SignedUpdateInstaller{}, err
	}
	services := SystemdManager{}
	return SignedUpdateInstaller{
		Paths: layout.Production(), ExpectedUID: 0, PublicKey: publicKey,
		Executable: executable, ManifestSource: manifestSource, BinaryPath: binaryPath,
		AcquireLock: func(path string, expectedUID int) (io.Closer, error) {
			return singletonlock.Acquire(path, expectedUID)
		},
		StartService: services.Start,
	}, nil
}

func (installer SignedUpdateInstaller) Run(ctx context.Context) error {
	if installer.Paths.ReleasesRoot == "" || installer.Paths.Current == "" || installer.Paths.Previous == "" ||
		installer.Paths.DaemonLock == "" || installer.ExpectedUID < 0 || len(installer.PublicKey) != ed25519.PublicKeySize ||
		installer.Executable == "" || installer.ManifestSource == "" || installer.AcquireLock == nil || installer.StartService == nil {
		return errors.New("signed update installer configuration is incomplete")
	}
	if os.Geteuid() != installer.ExpectedUID {
		return errors.New("signed update installer must run as the installation owner")
	}
	lock, err := installer.AcquireLock(installer.Paths.DaemonLock, installer.ExpectedUID)
	if err != nil {
		return fmt.Errorf("require stopped platformd unit: %w", err)
	}
	lockOpen := true
	defer func() {
		if lockOpen {
			_ = lock.Close()
		}
	}()

	current, err := CurrentReleaseManifest(installer.Paths, installer.PublicKey, installer.ExpectedUID)
	if err != nil {
		return err
	}
	if err := installer.verifyPreviousExecutable(current.Version); err != nil {
		return err
	}
	manifestBytes, err := installer.loadManifest(ctx)
	if err != nil {
		return err
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, installer.PublicKey)
	if err != nil {
		return err
	}
	if err := manifest.AllowsUpdateFrom(current.Version); err != nil {
		return err
	}

	binaryPath := installer.BinaryPath
	if binaryPath == "" {
		binaryPath, err = installer.downloadBinary(ctx, manifest)
		if err != nil {
			return err
		}
		defer os.Remove(binaryPath)
	}
	release, err := VerifyRelease(binaryPath, manifestBytes, installer.PublicKey)
	if err != nil {
		return err
	}
	if err := PublishReleaseSlot(release, installer.Paths, installer.ExpectedUID); err != nil {
		return err
	}
	if err := SwitchToRelease(installer.Paths, current.Version, release.Manifest.Version, installer.PublicKey, installer.ExpectedUID); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	lockOpen = false
	return installer.StartService(ctx)
}

func (installer SignedUpdateInstaller) verifyPreviousExecutable(currentVersion string) error {
	previousName, err := os.Readlink(installer.Paths.Previous)
	if err != nil || !validReleaseName(previousName) || previousName == currentVersion {
		return errors.New("saved previous release is unavailable or invalid")
	}
	previousSlot := filepath.Join(installer.Paths.ReleasesRoot, previousName)
	if err := VerifyReleaseSlot(previousSlot, nil, installer.PublicKey, installer.ExpectedUID); err != nil {
		return err
	}
	executable, err := filepath.EvalSymlinks(installer.Executable)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(previousSlot, "platformd"))
	if err != nil || executable != expected {
		return errors.New("signed forward update must be installed by the saved previous binary")
	}
	return nil
}

func (installer SignedUpdateInstaller) loadManifest(ctx context.Context) ([]byte, error) {
	parsed, err := url.Parse(installer.ManifestSource)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" {
		info, err := os.Lstat(installer.ManifestSource)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("local release manifest is not a regular file")
		}
		return readBoundedFile(installer.ManifestSource, maximumReleaseManifestBytes)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("release manifest source must be a local file or HTTPS URL")
	}
	request, err := releaseRequest(ctx, installer.ManifestSource, "application/json")
	if err != nil {
		return nil, err
	}
	response, err := installer.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL.Scheme != "https" || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release manifest response is invalid: status %d", response.StatusCode)
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, maximumReleaseManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(value) > maximumReleaseManifestBytes {
		return nil, errors.New("release manifest exceeds size limit")
	}
	return value, nil
}

func (installer SignedUpdateInstaller) downloadBinary(ctx context.Context, manifest releasemanifest.Manifest) (string, error) {
	if err := os.MkdirAll(installer.Paths.ReleasesRoot, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(installer.Paths.ReleasesRoot, ".forward-download-")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func(err error) (string, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	request, err := releaseRequest(ctx, manifest.BinaryURL, "application/octet-stream")
	if err != nil {
		return cleanup(err)
	}
	response, err := installer.client().Do(request)
	if err != nil {
		return cleanup(err)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL.Scheme != "https" || response.StatusCode != http.StatusOK {
		return cleanup(fmt.Errorf("release binary response is invalid: status %d", response.StatusCode))
	}
	chmodErr := file.Chmod(0o700)
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, manifest.BinarySize+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if chmodErr != nil || copyErr != nil || syncErr != nil || closeErr != nil {
		return cleanup(errors.Join(chmodErr, copyErr, syncErr, closeErr))
	}
	if written != manifest.BinarySize {
		return cleanup(fmt.Errorf("release binary response size = %d, want %d", written, manifest.BinarySize))
	}
	return path, nil
}

func (installer SignedUpdateInstaller) client() *http.Client {
	if installer.HTTPClient != nil {
		return installer.HTTPClient
	}
	return &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 || request.URL.Scheme != "https" {
				return errors.New("release redirect is not allowed")
			}
			return nil
		},
	}
}

func releaseRequest(ctx context.Context, target, accept string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	return request, nil
}
