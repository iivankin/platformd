package registry

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/iivankin/platformd/internal/registryauth"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

type CreateCredentialInput struct {
	RepositoryID string
	Name         string
	Permission   string
	Actor        Actor
}

type CreateCredentialResult struct {
	Credential state.RegistryCredential
	Username   string
	Secret     string
	RequestID  string
}

type CredentialDetails struct {
	Credential      state.RegistryCredential
	Username        string
	Secret          string
	SecretAvailable bool
}

func (application *Application) MarkCredentialUsed(ctx context.Context, credentialID string) error {
	return application.store.TouchRegistryCredentialLastUsed(ctx, credentialID, application.now().UnixMilli())
}

func (application *Application) Credentials(ctx context.Context, repositoryID string) ([]state.RegistryCredential, error) {
	if _, err := application.store.RegistryRepository(ctx, repositoryID); err != nil {
		return nil, err
	}
	return application.store.RegistryCredentials(ctx, repositoryID)
}

func (application *Application) CredentialDetails(ctx context.Context, repositoryID string) ([]CredentialDetails, error) {
	credentials, err := application.Credentials(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	details := make([]CredentialDetails, 0, len(credentials))
	for _, credential := range credentials {
		username, err := registryauth.Username(credential.ID)
		if err != nil {
			return nil, err
		}
		entry := CredentialDetails{Credential: credential, Username: username}
		if len(credential.SecretEncrypted) != 0 {
			entry.Secret, err = registryauth.OpenSecret(application.master, repositoryID, credential.ID, credential.SecretEncrypted)
			if err != nil {
				return nil, err
			}
			entry.SecretAvailable = true
		}
		details = append(details, entry)
	}
	return details, nil
}

func (application *Application) CreateCredential(ctx context.Context, input CreateCredentialInput) (CreateCredentialResult, error) {
	if input.RepositoryID == "" || input.Actor.ID == "" || (input.Actor.Kind != "access" && input.Actor.Kind != "token") || (input.Actor.Kind == "access" && input.Actor.Email == "") {
		return CreateCredentialResult{}, fmt.Errorf("%w: credential actor or repository is incomplete", ErrInvalidInput)
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return CreateCredentialResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.Permission != "pull" && input.Permission != "pull_push" {
		return CreateCredentialResult{}, fmt.Errorf("%w: permission must be pull or pull_push", ErrInvalidInput)
	}
	if _, err := application.store.RegistryRepository(ctx, input.RepositoryID); err != nil {
		return CreateCredentialResult{}, err
	}
	now := application.now()
	identifiers, err := application.identifiers(now, 3)
	if err != nil {
		return CreateCredentialResult{}, err
	}
	username, err := registryauth.Username(identifiers[0])
	if err != nil {
		return CreateCredentialResult{}, err
	}
	secret, err := registryauth.GenerateSecret(application.random)
	if err != nil {
		return CreateCredentialResult{}, err
	}
	verifier, err := registryauth.Verifier(application.master, input.RepositoryID, identifiers[0], secret)
	if err != nil {
		return CreateCredentialResult{}, err
	}
	encrypted, err := registryauth.SealSecret(application.master, input.RepositoryID, identifiers[0], secret)
	if err != nil {
		return CreateCredentialResult{}, err
	}
	credential, err := application.store.CreateRegistryCredential(ctx, state.CreateRegistryCredential{
		ID: identifiers[0], RepositoryID: input.RepositoryID, Name: input.Name,
		Permission: input.Permission, SecretHMAC: verifier, SecretEncrypted: encrypted,
		AuditEventID: identifiers[1],
		ActorKind:    input.Actor.Kind, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: identifiers[2], CreatedAtMillis: now.UnixMilli(),
	})
	if err != nil {
		return CreateCredentialResult{}, err
	}
	return CreateCredentialResult{
		Credential: credential, Username: username, Secret: secret, RequestID: identifiers[2],
	}, nil
}

func (application *Application) DeleteCredential(ctx context.Context, repositoryID, credentialID string, actor Actor) (string, error) {
	if repositoryID == "" || credentialID == "" || actor.ID == "" || (actor.Kind != "access" && actor.Kind != "token") || (actor.Kind == "access" && actor.Email == "") {
		return "", fmt.Errorf("%w: credential deletion input is incomplete", ErrInvalidInput)
	}
	now := application.now()
	identifiers, err := application.identifiers(now, 2)
	if err != nil {
		return "", err
	}
	uploads, err := application.store.DeleteRegistryCredential(ctx, state.DeleteRegistryCredential{
		RepositoryID: repositoryID, CredentialID: credentialID, AuditEventID: identifiers[0],
		ActorKind: actor.Kind, ActorID: actor.ID, ActorEmail: actor.Email,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: now.UnixMilli(),
	})
	if err != nil {
		return "", err
	}
	var cleanupErrors []error
	for _, upload := range uploads {
		lock := application.acquireUploadLock(upload.ID)
		size, sizeErr := application.payloads.UploadSize(upload.RepositoryID, upload.ID)
		removeErr := application.payloads.Cancel(upload.RepositoryID, upload.ID)
		application.releaseUploadLock(upload.ID, lock)
		if sizeErr == nil && removeErr == nil {
			application.temporaryBytes.release(size)
		}
		if sizeErr != nil && !errors.Is(sizeErr, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, sizeErr)
		}
		if removeErr != nil {
			cleanupErrors = append(cleanupErrors, removeErr)
		}
	}
	return identifiers[1], errors.Join(cleanupErrors...)
}
