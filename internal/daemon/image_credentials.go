package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type liveImageCredentialRepository struct {
	store  *state.Store
	master cryptobox.MasterKey
}

func (repository liveImageCredentialRepository) Resolve(ctx context.Context, service state.ServiceDesired) (deployment.ImageCredential, error) {
	if service.Snapshot.Source.Type != servicesource.PrivateImage {
		return deployment.ImageCredential{}, nil
	}
	credential, err := repository.store.ServiceImageCredential(ctx, service.ID)
	if err != nil {
		return deployment.ImageCredential{}, fmt.Errorf("load service image credential: %w", err)
	}
	imageHost, err := imagecredential.HostForReference(servicesource.ImageReference(service.Snapshot.Source))
	if err != nil {
		return deployment.ImageCredential{}, err
	}
	if credential.RegistryHost != imageHost {
		return deployment.ImageCredential{}, fmt.Errorf("image credential is for %s, image uses %s", credential.RegistryHost, imageHost)
	}
	password, err := imagecredential.OpenPassword(repository.master, credential.ServiceID, credential.PasswordEncrypted)
	if err != nil {
		return deployment.ImageCredential{}, fmt.Errorf("decrypt image credential: %w", err)
	}
	return deployment.ImageCredential{Username: credential.Username, Password: password}, nil
}

func (repository liveImageCredentialRepository) PrepareServiceImageCredential(
	ctx context.Context,
	input server.ServiceImageCredentialInput,
) (*state.ServiceImageCredential, error) {
	host, err := imagecredential.HostForReference(input.ImageReference)
	if err != nil {
		return nil, err
	}
	username := input.Username
	password := input.Password
	if password == "" {
		existing, loadErr := repository.store.ServiceImageCredential(ctx, input.ServiceID)
		if errors.Is(loadErr, sql.ErrNoRows) {
			return nil, imagecredential.ValidatePassword(password)
		}
		if loadErr != nil {
			return nil, fmt.Errorf("load existing service image credential: %w", loadErr)
		}
		if existing.RegistryHost != host {
			return nil, errors.New("a password is required after changing the private registry host")
		}
		if username == "" {
			username = existing.Username
		}
		if err := imagecredential.ValidateUsername(username); err != nil {
			return nil, err
		}
		return &state.ServiceImageCredential{
			ServiceID: input.ServiceID, RegistryHost: host, Username: username,
			PasswordEncrypted: existing.PasswordEncrypted, UpdatedAtMillis: input.UpdatedAtMillis,
		}, nil
	}
	if err := imagecredential.ValidateAuthentication(username, password); err != nil {
		return nil, err
	}
	encrypted, err := imagecredential.SealPassword(repository.master, input.ServiceID, password)
	if err != nil {
		return nil, err
	}
	return &state.ServiceImageCredential{
		ServiceID: input.ServiceID, RegistryHost: host, Username: username,
		PasswordEncrypted: encrypted, UpdatedAtMillis: input.UpdatedAtMillis,
	}, nil
}

func (repository liveImageCredentialRepository) RevealServiceImageCredential(ctx context.Context, serviceID string) (string, string, string, error) {
	credential, err := repository.store.ServiceImageCredential(ctx, serviceID)
	if err != nil {
		return "", "", "", err
	}
	password, err := imagecredential.OpenPassword(repository.master, serviceID, credential.PasswordEncrypted)
	if err != nil {
		return "", "", "", err
	}
	return credential.RegistryHost, credential.Username, password, nil
}
