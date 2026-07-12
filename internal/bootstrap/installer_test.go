package bootstrap_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/state"
)

type serviceManagerStub struct {
	reloads        int
	starts         int
	healthChecks   int
	healthHostname string
}

func (manager *serviceManagerStub) ReloadAndEnable(context.Context) error {
	manager.reloads++
	return nil
}

func (manager *serviceManagerStub) Start(context.Context) error {
	manager.starts++
	return nil
}

func (manager *serviceManagerStub) Health(_ context.Context, hostname, certificate string) error {
	manager.healthChecks++
	manager.healthHostname = hostname
	if certificate == "" {
		return errors.New("missing health certificate")
	}
	return nil
}

func TestInstallerCreatesCompleteStateAndRepairsWithoutInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths := layout.FromRoots(
		filepath.Join(root, "data"),
		filepath.Join(root, "config"),
		filepath.Join(root, "bin", "platformd"),
		filepath.Join(root, "systemd", "platformd.service"),
	)
	release, publicKey := testVerifiedRelease(t, root)
	certificate, privateKey := testCertificate(t, []string{"*.example.com"})
	validated, err := bootstrap.ValidateInput(bootstrap.Input{
		AdminHostname:        "admin.example.com",
		AutomationHostname:   "api.example.com",
		AccessTeamDomain:     "team.cloudflareaccess.com",
		AccessAudience:       "audience",
		ConsolePassphrase:    "passphrase",
		OriginCertificatePEM: certificate,
		OriginPrivateKeyPEM:  privateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	services := &serviceManagerStub{}
	confirmations := 0
	inputs := 0
	repairProbe := false
	installer := bootstrap.Installer{
		Paths:        paths,
		ExpectedUID:  os.Getuid(),
		Random:       rand.Reader,
		Now:          func() time.Time { return time.Unix(1_800_000_000, 0) },
		LoadRelease:  func(context.Context) (bootstrap.VerifiedRelease, error) { return release, nil },
		ValidateHost: func(_ context.Context, _ string, repair bool) error { repairProbe = repair; return nil },
		ConfirmRecovery: func(value string) error {
			confirmations++
			if value == "" {
				return errors.New("empty recovery key")
			}
			return nil
		},
		ProvideInput:     func() (bootstrap.ValidatedInput, error) { inputs++; return validated, nil },
		Services:         services,
		ReleasePublicKey: publicKey,
	}
	if err := installer.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repairProbe || confirmations != 1 || inputs != 1 || services.reloads != 1 || services.starts != 1 || services.healthChecks != 1 {
		t.Fatalf("initial calls repair=%v confirmations=%d inputs=%d services=%+v", repairProbe, confirmations, inputs, services)
	}
	store, err := state.Open(context.Background(), paths.StateDatabase, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.Installation(context.Background())
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if installation.AdminHostname != "admin.example.com" || installation.AccessTeamDomain != "team.cloudflareaccess.com" || len(installation.OriginCertificates) != 1 {
		t.Fatalf("installation = %+v", installation)
	}

	installer.LoadRelease = func(context.Context) (bootstrap.VerifiedRelease, error) {
		t.Fatal("repair downloaded/replaced the release")
		return bootstrap.VerifiedRelease{}, nil
	}
	installer.ConfirmRecovery = func(string) error {
		t.Fatal("repair requested recovery confirmation")
		return nil
	}
	installer.ProvideInput = func() (bootstrap.ValidatedInput, error) {
		t.Fatal("repair requested initialization input")
		return bootstrap.ValidatedInput{}, nil
	}
	if err := installer.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !repairProbe || services.reloads != 2 || services.starts != 2 || services.healthChecks != 2 || services.healthHostname != "admin.example.com" {
		t.Fatalf("repair calls repair=%v services=%+v", repairProbe, services)
	}
}

func testVerifiedRelease(t *testing.T, root string) (bootstrap.VerifiedRelease, ed25519.PublicKey) {
	t.Helper()
	executable := filepath.Join(root, "source-platformd")
	if err := os.WriteFile(executable, []byte("\x7fELFplatformd"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Join(root, "runtime-source")
	if err := os.Mkdir(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDirectory, "crun"), []byte("runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Append(executable, runtimeDirectory); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture: "amd64",
		BinarySHA256: hex.EncodeToString(hash[:]),
		BinarySize:   int64(len(binary)),
		BinaryURL:    "https://example.com/platformd",
		Format:       1,
		OS:           "linux",
		Version:      "1.0.0",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return bootstrap.VerifiedRelease{
		ExecutablePath: executable,
		Manifest:       manifest,
		ManifestBytes:  manifestBytes,
		PublicKey:      publicKey,
	}, publicKey
}
