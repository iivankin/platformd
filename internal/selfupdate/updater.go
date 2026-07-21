package selfupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/semver"
)

const maximumManifestBytes = 64 << 10

var ErrUpToDate = errors.New("platform is already up to date")

type ResumeWorkloads func(context.Context) error
type QuiesceWorkloads func(context.Context) (ResumeWorkloads, error)
type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type Config struct {
	Paths            layout.Paths
	ExpectedUID      int
	ManifestURL      string
	PublicKey        ed25519.PublicKey
	HTTPClient       *http.Client
	Admission        *admission.Gate
	Growth           GrowthGate
	QuiesceWorkloads QuiesceWorkloads
}

type Result struct {
	PreviousVersion string `json:"previousVersion"`
	TargetVersion   string `json:"targetVersion"`
}

type Status struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	UpdateSupported bool   `json:"updateSupported"`
}

type BusyError struct {
	Snapshot admission.Snapshot
}

func (failure BusyError) Error() string { return admission.ErrBusy.Error() }
func (failure BusyError) Unwrap() error { return admission.ErrBusy }

type Updater struct {
	config Config
	client *http.Client
}

type candidate struct {
	current       releasemanifest.Manifest
	target        releasemanifest.Manifest
	manifestBytes []byte
	binaryPath    string
	publicKey     ed25519.PublicKey
}

func New(config Config) (*Updater, error) {
	if config.Paths.ReleasesRoot == "" || config.Paths.Current == "" || config.Paths.Previous == "" ||
		config.ExpectedUID < 0 || len(config.PublicKey) != ed25519.PublicKeySize || config.Admission == nil ||
		config.Growth == nil || config.QuiesceWorkloads == nil {
		return nil, errors.New("self-update configuration is incomplete")
	}
	if err := validateHTTPS(config.ManifestURL); err != nil {
		return nil, fmt.Errorf("latest release manifest URL: %w", err)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(request *http.Request, previous []*http.Request) error {
				if len(previous) >= 5 || request.URL.Scheme != "https" {
					return errors.New("release redirect is not allowed")
				}
				return nil
			},
		}
	}
	return &Updater{config: config, client: client}, nil
}

func (updater *Updater) Check(ctx context.Context) (Status, error) {
	current, target, _, err := updater.manifests(ctx)
	if err != nil {
		return Status{}, err
	}
	available, err := newerThan(target.Version, current.Version)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		CurrentVersion: current.Version, LatestVersion: target.Version,
		UpdateAvailable: available, UpdateSupported: true,
	}
	if available && target.AllowsUpdateFrom(current.Version) != nil {
		status.UpdateSupported = false
	}
	return status, nil
}

func (updater *Updater) Apply(ctx context.Context) (Result, error) {
	if err := updater.config.Growth.PermitGrowth(ctx); err != nil {
		return Result{}, err
	}
	candidate, err := updater.prepare(ctx)
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(candidate.binaryPath)

	updateLease, blockers, err := updater.config.Admission.TryUpdate()
	if err != nil {
		if errors.Is(err, admission.ErrBusy) {
			return Result{}, BusyError{Snapshot: blockers}
		}
		return Result{}, err
	}
	committed := false
	released := false
	releaseUpdate := func() {
		if !released {
			updateLease.Release()
			released = true
		}
	}
	defer func() {
		if !committed {
			releaseUpdate()
		}
	}()

	release := bootstrap.VerifiedRelease{
		ExecutablePath: candidate.binaryPath,
		Manifest:       candidate.target,
		ManifestBytes:  candidate.manifestBytes,
		PublicKey:      candidate.publicKey,
	}
	if err := bootstrap.PublishReleaseSlot(release, updater.config.Paths, updater.config.ExpectedUID); err != nil {
		return Result{}, fmt.Errorf("stage release slot: %w", err)
	}
	resume, err := updater.config.QuiesceWorkloads(ctx)
	if err != nil {
		releaseUpdate()
		if resume != nil {
			err = errors.Join(err, resume(ctx))
		}
		return Result{}, fmt.Errorf("stop workloads before update: %w", err)
	}
	if err := bootstrap.SwitchToRelease(
		updater.config.Paths, candidate.current.Version, candidate.target.Version,
		candidate.publicKey, updater.config.ExpectedUID,
	); err != nil {
		releaseUpdate()
		if resume != nil {
			err = errors.Join(err, resume(ctx))
		}
		return Result{}, err
	}
	committed = true
	return Result{PreviousVersion: candidate.current.Version, TargetVersion: candidate.target.Version}, nil
}

func (updater *Updater) prepare(ctx context.Context) (candidate, error) {
	current, target, manifestBytes, err := updater.manifests(ctx)
	if err != nil {
		return candidate{}, err
	}
	available, err := newerThan(target.Version, current.Version)
	if err != nil {
		return candidate{}, err
	}
	if !available {
		return candidate{}, ErrUpToDate
	}
	if err := target.AllowsUpdateFrom(current.Version); err != nil {
		return candidate{}, err
	}
	if err := os.MkdirAll(updater.config.Paths.ReleasesRoot, 0o700); err != nil {
		return candidate{}, err
	}
	binary, err := os.CreateTemp(updater.config.Paths.ReleasesRoot, ".download-")
	if err != nil {
		return candidate{}, fmt.Errorf("create release download: %w", err)
	}
	binaryPath := binary.Name()
	if err := binary.Close(); err != nil {
		_ = os.Remove(binaryPath)
		return candidate{}, err
	}
	cleanup := func(err error) (candidate, error) {
		_ = os.Remove(binaryPath)
		return candidate{}, err
	}
	if err := updater.downloadBinary(ctx, target, binaryPath); err != nil {
		return cleanup(err)
	}
	bundle, err := releasebundle.Open(binaryPath)
	if err != nil {
		return cleanup(err)
	}
	verifyErr := bundle.Verify()
	closeErr := bundle.Close()
	if verifyErr != nil || closeErr != nil {
		return cleanup(errors.Join(verifyErr, closeErr))
	}
	return candidate{
		current: current, target: target, manifestBytes: append([]byte(nil), manifestBytes...),
		binaryPath: binaryPath, publicKey: append(ed25519.PublicKey(nil), updater.config.PublicKey...),
	}, nil
}

func (updater *Updater) manifests(ctx context.Context) (releasemanifest.Manifest, releasemanifest.Manifest, []byte, error) {
	current, err := bootstrap.CurrentReleaseManifest(updater.config.Paths, updater.config.PublicKey, updater.config.ExpectedUID)
	if err != nil {
		return releasemanifest.Manifest{}, releasemanifest.Manifest{}, nil, fmt.Errorf("verify current release: %w", err)
	}
	manifestBytes, err := updater.fetch(ctx, updater.config.ManifestURL, maximumManifestBytes)
	if err != nil {
		return releasemanifest.Manifest{}, releasemanifest.Manifest{}, nil, fmt.Errorf("fetch latest release manifest: %w", err)
	}
	target, err := releasemanifest.ParseAndVerify(manifestBytes, updater.config.PublicKey)
	if err != nil {
		return releasemanifest.Manifest{}, releasemanifest.Manifest{}, nil, err
	}
	return current, target, manifestBytes, nil
}

func newerThan(candidateVersion, currentVersion string) (bool, error) {
	candidate, err := semver.Parse(candidateVersion)
	if err != nil {
		return false, fmt.Errorf("candidate version: %w", err)
	}
	current, err := semver.Parse(currentVersion)
	if err != nil {
		return false, fmt.Errorf("current version: %w", err)
	}
	return semver.Compare(candidate, current) > 0, nil
}

func (updater *Updater) downloadBinary(ctx context.Context, manifest releasemanifest.Manifest, destination string) error {
	if err := validateHTTPS(manifest.BinaryURL); err != nil {
		return err
	}
	response, err := updater.get(ctx, manifest.BinaryURL, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("fetch release binary: %w", err)
	}
	defer response.Body.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	chmodErr := file.Chmod(0o700)
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, manifest.BinarySize+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if chmodErr != nil || copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(chmodErr, copyErr, syncErr, closeErr)
	}
	if written != manifest.BinarySize {
		return fmt.Errorf("release binary response size = %d, want %d", written, manifest.BinarySize)
	}
	return manifest.VerifyBinary(destination)
}

func (updater *Updater) fetch(ctx context.Context, value string, maximum int64) ([]byte, error) {
	response, err := updater.get(ctx, value, "application/json")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, errors.New("release response exceeds size limit")
	}
	return content, nil
}

func (updater *Updater) get(ctx context.Context, value, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	response, err := updater.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.Request == nil || response.Request.URL.Scheme != "https" || response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("release response status or transport is invalid: %d", response.StatusCode)
	}
	return response, nil
}

func validateHTTPS(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("URL must be HTTPS without userinfo or fragment")
	}
	return nil
}
