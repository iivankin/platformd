package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"

	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/state"
)

const (
	MaximumManifestSize           = 4 << 20
	MaximumManifestReferences     = 10_000
	MaximumManifestsPerRepository = 100_000
	OCIImageManifestMediaType     = "application/vnd.oci.image.manifest.v1+json"
	OCIImageIndexMediaType        = "application/vnd.oci.image.index.v1+json"
	DockerImageManifestMediaType  = "application/vnd.docker.distribution.manifest.v2+json"
	DockerManifestListMediaType   = "application/vnd.docker.distribution.manifest.list.v2+json"
)

type manifestDescriptor struct {
	Digest   string        `json:"digest"`
	Platform ImagePlatform `json:"platform"`
}

type manifestDocument struct {
	SchemaVersion int                  `json:"schemaVersion"`
	MediaType     string               `json:"mediaType"`
	Config        manifestDescriptor   `json:"config"`
	Layers        []manifestDescriptor `json:"layers"`
	Manifests     []manifestDescriptor `json:"manifests"`
}

func (application *Application) PutManifest(ctx context.Context, authentication Authentication, reference, contentType string, body []byte) (state.RegistryManifest, error) {
	if authentication.Credential.Permission != "pull_push" || authentication.Repository.ID != authentication.Credential.RepositoryID {
		return state.RegistryManifest{}, ErrDenied
	}
	lock := application.acquireRepositoryLock(authentication.Repository.ID)
	defer application.releaseRepositoryLock(authentication.Repository.ID, lock)
	mediaType, references, index, err := validateManifest(contentType, body)
	if err != nil {
		return state.RegistryManifest{}, err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	tag := ""
	if registryname.ValidateDigest(reference) == nil {
		if reference != digest {
			return state.RegistryManifest{}, fmt.Errorf("%w: manifest digest reference does not match body", ErrInvalidInput)
		}
	} else {
		if err := registryname.ValidateTag(reference); err != nil {
			return state.RegistryManifest{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		tag = reference
	}
	for _, referencedDigest := range references {
		if index {
			exists, err := application.store.RegistryManifestExists(ctx, authentication.Repository.ID, referencedDigest)
			if err != nil {
				return state.RegistryManifest{}, err
			}
			if !exists {
				return state.RegistryManifest{}, fmt.Errorf("%w: child manifest %s is missing", ErrInvalidInput, referencedDigest)
			}
			continue
		}
		exists, err := application.payloads.BlobExists(authentication.Repository.ID, referencedDigest)
		if err != nil {
			return state.RegistryManifest{}, err
		}
		if !exists {
			return state.RegistryManifest{}, fmt.Errorf("%w: blob %s is missing", ErrInvalidInput, referencedDigest)
		}
	}
	manifest, err := application.store.PutRegistryManifest(ctx, state.PutRegistryManifest{
		RepositoryID: authentication.Repository.ID, Digest: digest, MediaType: mediaType,
		Body: body, Tag: tag, PushedAtMillis: application.now().UnixMilli(),
		MaximumForRepository: MaximumManifestsPerRepository,
	})
	if errors.Is(err, state.ErrRegistryManifestQuota) {
		return state.RegistryManifest{}, ErrManifestQuota
	}
	if err == nil && tag != "" && application.publisher != nil {
		application.publisher.RegistryTagPublished(authentication.Repository.Name, tag)
	}
	return manifest, err
}

func (application *Application) Manifest(ctx context.Context, repositoryID, reference string) (state.RegistryManifest, error) {
	return application.store.RegistryManifest(ctx, repositoryID, reference)
}

func (application *Application) Tags(ctx context.Context, repositoryID, after string, limit int) ([]state.RegistryTag, bool, error) {
	return application.store.RegistryTags(ctx, repositoryID, after, limit)
}

func (application *Application) OpenBlob(repositoryID, digest string) (io.ReadSeekCloser, int64, error) {
	return application.payloads.OpenBlob(repositoryID, digest)
}

func validateManifest(contentType string, body []byte) (string, []string, bool, error) {
	if len(body) == 0 || len(body) > MaximumManifestSize {
		return "", nil, false, fmt.Errorf("%w: manifest must be 1..4 MiB", ErrInvalidInput)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", nil, false, fmt.Errorf("%w: manifest Content-Type is invalid", ErrInvalidInput)
	}
	var document manifestDocument
	if err := json.Unmarshal(body, &document); err != nil || document.SchemaVersion != 2 {
		return "", nil, false, fmt.Errorf("%w: manifest JSON or schemaVersion is invalid", ErrInvalidInput)
	}
	if document.MediaType != "" && document.MediaType != mediaType {
		return "", nil, false, fmt.Errorf("%w: manifest mediaType differs from Content-Type", ErrInvalidInput)
	}
	index := mediaType == OCIImageIndexMediaType || mediaType == DockerManifestListMediaType
	image := mediaType == OCIImageManifestMediaType || mediaType == DockerImageManifestMediaType
	if !index && !image {
		return "", nil, false, fmt.Errorf("%w: manifest media type is unsupported", ErrInvalidInput)
	}
	var descriptors []manifestDescriptor
	if index {
		if document.Config.Digest != "" || len(document.Layers) != 0 || len(document.Manifests) == 0 {
			return "", nil, false, fmt.Errorf("%w: index descriptors are invalid", ErrInvalidInput)
		}
		descriptors = document.Manifests
	} else {
		if document.Config.Digest == "" || len(document.Manifests) != 0 {
			return "", nil, false, fmt.Errorf("%w: image descriptors are invalid", ErrInvalidInput)
		}
		descriptors = append([]manifestDescriptor{document.Config}, document.Layers...)
	}
	if len(descriptors) > MaximumManifestReferences {
		return "", nil, false, fmt.Errorf("%w: manifest reference limit exceeded", ErrInvalidInput)
	}
	references := make([]string, len(descriptors))
	for index, descriptor := range descriptors {
		if err := registryname.ValidateDigest(descriptor.Digest); err != nil {
			return "", nil, false, fmt.Errorf("%w: descriptor digest is invalid", ErrInvalidInput)
		}
		references[index] = descriptor.Digest
	}
	return mediaType, references, index, nil
}
