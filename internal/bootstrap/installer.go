package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/hostcheck"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/passphrase"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/state"
)

type ServiceManager interface {
	ReloadAndEnable(context.Context) error
	Start(context.Context) error
	Health(context.Context, string, string) error
}

type Installer struct {
	Paths            layout.Paths
	ExpectedUID      int
	Random           io.Reader
	Now              func() time.Time
	LoadRelease      func(context.Context) (VerifiedRelease, error)
	ValidateHost     func(context.Context, string, bool) error
	ConfirmRecovery  func(string) error
	ProvideInput     func() (ValidatedInput, error)
	Services         ServiceManager
	ReleasePublicKey ed25519.PublicKey
}

func ProductionInstaller(confirm func(string) error, provideInput func() (ValidatedInput, error)) Installer {
	publicKey, _ := releaseconfig.PublicKey()
	return Installer{
		Paths:            layout.Production(),
		ExpectedUID:      0,
		Random:           rand.Reader,
		Now:              time.Now,
		LoadRelease:      LoadProductionRelease,
		ValidateHost:     validateProductionHost,
		ConfirmRecovery:  confirm,
		ProvideInput:     provideInput,
		Services:         SystemdManager{},
		ReleasePublicKey: publicKey,
	}
}

func (installer Installer) Init(ctx context.Context) error {
	if err := installer.validateConfiguration(); err != nil {
		return err
	}
	installation, complete, err := installer.existingInstallation(ctx)
	if err != nil {
		return err
	}
	if complete {
		return installer.repair(ctx, installation)
	}
	if err := installer.ValidateHost(ctx, nearestExistingParent(installer.Paths.DataRoot), false); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(installer.Paths.DataRoot, installer.ExpectedUID); err != nil {
		return err
	}
	release, err := installer.LoadRelease(ctx)
	if err != nil {
		return err
	}
	if err := PublishReleaseSlot(release, installer.Paths, installer.ExpectedUID); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(installer.Paths.ConfigRoot, installer.ExpectedUID); err != nil {
		return err
	}
	master, _, err := masterkey.LoadOrCreate(installer.Paths.MasterKey, installer.ExpectedUID, installer.Random)
	if err != nil {
		return fmt.Errorf("initialize master key: %w", err)
	}
	store, err := state.Open(ctx, installer.Paths.StateDatabase, installer.ExpectedUID)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := installer.ConfirmRecovery(masterkey.RecoveryString(master)); err != nil {
		return err
	}
	input, err := installer.ProvideInput()
	if err != nil {
		return err
	}
	defer clear(input.ConsolePassphrase)
	defer clear(input.OriginPrivateKeyPEM)

	initial, err := installer.prepareInstallation(master, input)
	if err != nil {
		return err
	}
	if err := SwitchCurrentRelease(installer.Paths, release.Manifest.Version); err != nil {
		return err
	}
	if err := installLocalBinaryLink(installer.Paths); err != nil {
		return err
	}
	if err := installSystemdUnit(installer.Paths, installer.ExpectedUID); err != nil {
		return err
	}
	if err := installer.Services.ReloadAndEnable(ctx); err != nil {
		return err
	}
	if err := store.CreateInstallation(ctx, initial); err != nil {
		return err
	}
	if err := installer.Services.Start(ctx); err != nil {
		return err
	}
	return installer.Services.Health(ctx, input.AdminHostname, input.OriginCertificatePEM)
}

func (installer Installer) prepareInstallation(master cryptobox.MasterKey, input ValidatedInput) (state.InitialInstallation, error) {
	timestamp := installer.Now()
	installationID, err := id.NewWith(timestamp, installer.Random)
	if err != nil {
		return state.InitialInstallation{}, err
	}
	certificateID, err := id.NewWith(timestamp, installer.Random)
	if err != nil {
		return state.InitialInstallation{}, err
	}
	auditID, err := id.NewWith(timestamp, installer.Random)
	if err != nil {
		return state.InitialInstallation{}, err
	}
	verifier, err := passphrase.HashWith(input.ConsolePassphrase, installer.Random)
	if err != nil {
		return state.InitialInstallation{}, err
	}
	box, err := cryptobox.NewBox(master, []byte(certificateID), "platformd/sqlite/origin-certificate/v1")
	if err != nil {
		return state.InitialInstallation{}, err
	}
	privateKey, err := box.SealWith(installer.Random, input.OriginPrivateKeyPEM, []byte(certificateID+":private-key"))
	if err != nil {
		return state.InitialInstallation{}, err
	}
	return state.InitialInstallation{
		ID:                   installationID,
		AdminHostname:        input.AdminHostname,
		AccessTeamDomain:     input.AccessTeamDomain,
		AccessAudience:       input.AccessAudience,
		ConsolePassphrasePHC: verifier,
		OriginCertificateID:  certificateID,
		OriginCertificatePEM: input.OriginCertificatePEM,
		OriginPrivateKey:     privateKey,
		InitialAuditEventID:  auditID,
		CreatedAtMillis:      timestamp.UnixMilli(),
	}, nil
}

func (installer Installer) existingInstallation(ctx context.Context) (state.Installation, bool, error) {
	if _, err := os.Lstat(installer.Paths.StateDatabase); errors.Is(err, os.ErrNotExist) {
		return state.Installation{}, false, nil
	} else if err != nil {
		return state.Installation{}, false, fmt.Errorf("inspect installation state: %w", err)
	}
	store, err := state.Open(ctx, installer.Paths.StateDatabase, installer.ExpectedUID)
	if err != nil {
		return state.Installation{}, false, err
	}
	defer store.Close()
	installation, err := store.Installation(ctx)
	if errors.Is(err, state.ErrNotInitialized) {
		return state.Installation{}, false, nil
	}
	if err != nil {
		return state.Installation{}, false, err
	}
	return installation, true, nil
}

func (installer Installer) repair(ctx context.Context, installation state.Installation) error {
	if err := installer.ValidateHost(ctx, nearestExistingParent(installer.Paths.DataRoot), true); err != nil {
		return err
	}
	if _, err := masterkey.Load(installer.Paths.MasterKey, installer.ExpectedUID); err != nil {
		return fmt.Errorf("load existing master key: %w", err)
	}
	if err := VerifyCurrentRelease(installer.Paths, installer.ReleasePublicKey, installer.ExpectedUID); err != nil {
		return err
	}
	if err := installLocalBinaryLink(installer.Paths); err != nil {
		return err
	}
	if err := installSystemdUnit(installer.Paths, installer.ExpectedUID); err != nil {
		return err
	}
	if err := installer.Services.ReloadAndEnable(ctx); err != nil {
		return err
	}
	if err := installer.Services.Start(ctx); err != nil {
		return err
	}
	certificatePEM, err := CertificateForHostname(installation.AdminHostname, installation.OriginCertificates)
	if err != nil {
		return err
	}
	return installer.Services.Health(ctx, installation.AdminHostname, certificatePEM)
}

func CertificateForHostname(hostname string, certificates []state.OriginCertificate) (string, error) {
	var wildcard string
	for _, certificate := range certificates {
		block, _ := pem.Decode([]byte(certificate.CertificatePEM))
		if block == nil || block.Type != "CERTIFICATE" {
			return "", fmt.Errorf("Origin certificate %s has invalid PEM", certificate.ID)
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parse Origin certificate %s: %w", certificate.ID, err)
		}
		if err := leaf.VerifyHostname(hostname); err != nil {
			continue
		}
		for _, name := range leaf.DNSNames {
			if strings.EqualFold(name, hostname) {
				return certificate.CertificatePEM, nil
			}
		}
		if wildcard == "" {
			wildcard = certificate.CertificatePEM
		}
	}
	if wildcard != "" {
		return wildcard, nil
	}
	return "", fmt.Errorf("no Origin certificate covers admin hostname %q", hostname)
}

func (installer Installer) validateConfiguration() error {
	if installer.Paths.DataRoot == "" || installer.Paths.ConfigRoot == "" || installer.ExpectedUID < 0 || installer.Random == nil || installer.Now == nil || installer.LoadRelease == nil || installer.ValidateHost == nil || installer.ConfirmRecovery == nil || installer.ProvideInput == nil || installer.Services == nil || len(installer.ReleasePublicKey) != ed25519.PublicKeySize {
		return errors.New("bootstrap installer configuration is incomplete")
	}
	return nil
}

func validateProductionHost(ctx context.Context, diskPath string, repair bool) error {
	facts, err := hostcheck.Collect(ctx, diskPath)
	if err != nil {
		return err
	}
	if repair {
		return facts.ValidateForRepair()
	}
	return facts.Validate()
}

func nearestExistingParent(value string) string {
	for {
		if _, err := os.Stat(value); err == nil {
			return value
		}
		parent := filepath.Dir(value)
		if parent == value {
			return value
		}
		value = parent
	}
}
