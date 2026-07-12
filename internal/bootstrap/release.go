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

	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/version"
)

const maximumReleaseManifestBytes = 64 << 10

type ReleaseLoaderConfig struct {
	ExecutablePath string
	Version        string
	ManifestURL    string
	PublicKey      ed25519.PublicKey
	HTTPClient     *http.Client
}

type VerifiedRelease struct {
	ExecutablePath string
	Manifest       releasemanifest.Manifest
	ManifestBytes  []byte
	PublicKey      ed25519.PublicKey
}

func LoadProductionRelease(ctx context.Context) (VerifiedRelease, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("locate running executable: %w", err)
	}
	executablePath, err = filepath.EvalSymlinks(executablePath)
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("resolve running executable: %w", err)
	}
	publicKey, err := releaseconfig.PublicKey()
	if err != nil {
		return VerifiedRelease{}, err
	}
	return LoadRelease(ctx, ReleaseLoaderConfig{
		ExecutablePath: executablePath,
		Version:        version.Version,
		ManifestURL:    releaseconfig.VersionManifestURL(version.Version),
		PublicKey:      publicKey,
	})
}

func LoadRelease(ctx context.Context, config ReleaseLoaderConfig) (VerifiedRelease, error) {
	if config.ExecutablePath == "" || config.Version == "" || config.ManifestURL == "" {
		return VerifiedRelease{}, errors.New("release loader configuration is incomplete")
	}
	manifestURL, err := url.Parse(config.ManifestURL)
	if err != nil || manifestURL.Scheme != "https" || manifestURL.Host == "" || manifestURL.User != nil || manifestURL.Fragment != "" {
		return VerifiedRelease{}, errors.New("release manifest URL must be HTTPS without userinfo or fragment")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(request *http.Request, previous []*http.Request) error {
				if len(previous) >= 5 || request.URL.Scheme != "https" {
					return errors.New("release manifest redirect is not allowed")
				}
				return nil
			},
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.ManifestURL, nil)
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("create release manifest request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("fetch release manifest: %w", err)
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL.Scheme != "https" {
		return VerifiedRelease{}, errors.New("release manifest response came from an insecure URL")
	}
	if response.StatusCode != http.StatusOK {
		return VerifiedRelease{}, fmt.Errorf("fetch release manifest: status %d", response.StatusCode)
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(response.Body, maximumReleaseManifestBytes+1))
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("read release manifest: %w", err)
	}
	if len(manifestBytes) > maximumReleaseManifestBytes {
		return VerifiedRelease{}, errors.New("release manifest exceeds 64 KiB")
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, config.PublicKey)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if manifest.Version != config.Version {
		return VerifiedRelease{}, fmt.Errorf("release manifest version = %s, running version = %s", manifest.Version, config.Version)
	}
	if err := manifest.VerifyBinary(config.ExecutablePath); err != nil {
		return VerifiedRelease{}, err
	}
	bundle, err := releasebundle.Open(config.ExecutablePath)
	if err != nil {
		return VerifiedRelease{}, err
	}
	verifyErr := bundle.Verify()
	closeErr := bundle.Close()
	if verifyErr != nil || closeErr != nil {
		return VerifiedRelease{}, errors.Join(verifyErr, closeErr)
	}
	return VerifiedRelease{
		ExecutablePath: config.ExecutablePath,
		Manifest:       manifest,
		ManifestBytes:  append([]byte(nil), manifestBytes...),
		PublicKey:      append(ed25519.PublicKey(nil), config.PublicKey...),
	}, nil
}

func (release VerifiedRelease) ExtractRuntime(root string) error {
	bundle, err := releasebundle.Open(release.ExecutablePath)
	if err != nil {
		return err
	}
	defer bundle.Close()
	return bundle.Extract(root)
}
