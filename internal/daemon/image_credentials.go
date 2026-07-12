package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/imagecredential"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type liveImageCredentialRepository struct {
	store  *state.Store
	master cryptobox.MasterKey
}

func (repository liveImageCredentialRepository) ImageCredentials(ctx context.Context, projectID string) ([]state.ImageRegistryCredential, error) {
	return repository.store.ImageRegistryCredentials(ctx, projectID)
}

func (repository liveImageCredentialRepository) CreateImageCredential(ctx context.Context, input server.CreateImageCredential) (state.ImageRegistryCredential, error) {
	if err := imagecredential.ValidateAuthentication(input.Username, input.Password); err != nil {
		return state.ImageRegistryCredential{}, err
	}
	registryHost, err := imagecredential.NormalizeHost(input.RegistryHost)
	if err != nil {
		return state.ImageRegistryCredential{}, err
	}
	encrypted, err := imagecredential.SealPassword(repository.master, input.ID, input.Password)
	if err != nil {
		return state.ImageRegistryCredential{}, err
	}
	return repository.store.CreateImageRegistryCredential(ctx, state.CreateImageRegistryCredential{
		ImageRegistryCredential: state.ImageRegistryCredential{
			ID: input.ID, ProjectID: input.ProjectID, Name: input.Name,
			RegistryHost: registryHost, Username: input.Username,
			PasswordEncrypted: encrypted, CreatedAtMillis: input.CreatedAtMillis,
		},
		AuditEventID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
		RequestCorrelationID: input.RequestCorrelationID,
	})
}

func (repository liveImageCredentialRepository) Resolve(ctx context.Context, service state.ServiceDesired) (deployment.ImageCredential, error) {
	credentialID := service.Snapshot.ImageCredentialID
	if credentialID == "" {
		return deployment.ImageCredential{}, nil
	}
	credential, err := repository.store.ImageRegistryCredential(ctx, credentialID)
	if err != nil {
		return deployment.ImageCredential{}, fmt.Errorf("load image credential: %w", err)
	}
	if credential.ProjectID != service.ProjectID {
		return deployment.ImageCredential{}, errors.New("image credential belongs to another project")
	}
	imageHost, err := imagecredential.HostForReference(service.Snapshot.ImageReference)
	if err != nil {
		return deployment.ImageCredential{}, err
	}
	if credential.RegistryHost != imageHost {
		return deployment.ImageCredential{}, fmt.Errorf("image credential is for %s, image uses %s", credential.RegistryHost, imageHost)
	}
	password, err := imagecredential.OpenPassword(repository.master, credential.ID, credential.PasswordEncrypted)
	if err != nil {
		return deployment.ImageCredential{}, fmt.Errorf("decrypt image credential: %w", err)
	}
	return deployment.ImageCredential{Username: credential.Username, Password: password}, nil
}
