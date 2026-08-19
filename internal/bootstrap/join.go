package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/iivankin/platformd/internal/hostagent"
	"github.com/iivankin/platformd/internal/resourcename"
)

type Joiner struct {
	Installer
	URL        string
	Token      string
	Name       string
	PublicIPv4 string
}

func ProductionJoiner(url, token, name, publicIPv4 string) Joiner {
	return Joiner{
		Installer:  ProductionInstaller(nil, nil),
		URL:        url,
		Token:      token,
		Name:       name,
		PublicIPv4: publicIPv4,
	}
}

func (joiner Joiner) Join(ctx context.Context) error {
	if joiner.Paths.ConfigRoot == "" || joiner.Services == nil || joiner.LoadRelease == nil || joiner.ValidateHost == nil {
		return errors.New("join installer configuration is incomplete")
	}
	if joiner.URL == "" || joiner.Token == "" {
		return errors.New("join URL and token are required")
	}
	if _, err := os.Lstat(joiner.Paths.MasterKey); err == nil {
		return errors.New("this machine already has a platformd control plane; join a child on a separate VPS")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(joiner.Paths.StateDatabase); err == nil {
		return errors.New("this machine already has platformd state; join a child on a separate VPS")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	name := joiner.Name
	if name == "" {
		name = hostagent.DefaultName()
	}
	if err := resourcename.Validate(name); err != nil {
		return fmt.Errorf("host name: %w", err)
	}
	if err := joiner.ValidateHost(ctx, nearestExistingParent(joiner.Paths.DataRoot), false); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(joiner.Paths.DataRoot, joiner.ExpectedUID); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(joiner.Paths.ConfigRoot, joiner.ExpectedUID); err != nil {
		return err
	}
	release, err := joiner.LoadRelease(ctx)
	if err != nil {
		return err
	}
	if err := PublishReleaseSlot(release, joiner.Paths, joiner.ExpectedUID); err != nil {
		return err
	}
	result, err := hostagent.Join(ctx, hostagent.JoinInput{
		URL: joiner.URL, Token: joiner.Token, Name: name, PublicIPv4: joiner.PublicIPv4,
	})
	if err != nil {
		return err
	}
	if err := SwitchCurrentRelease(joiner.Paths, release.Manifest.Version); err != nil {
		return err
	}
	if err := installLocalBinaryLink(joiner.Paths); err != nil {
		return err
	}
	if err := hostagent.Save(joiner.Paths.WorkerConfig, hostagent.Config{
		HostID: result.HostID, HostToken: result.HostToken,
		ParentHostname: result.ParentHostname, Name: result.Name,
	}); err != nil {
		return err
	}
	if err := installWorkerSystemdUnit(joiner.Paths, joiner.ExpectedUID); err != nil {
		return err
	}
	if err := joiner.Services.ReloadAndEnable(ctx); err != nil {
		return err
	}
	return joiner.Services.Start(ctx)
}
