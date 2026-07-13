package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/state"
)

const ociImageReferenceAnnotation = "org.opencontainers.image.ref.name"

type LocalPull struct {
	Reference string
	Digest    string
	Close     func()
}

type localIndex struct {
	SchemaVersion int               `json:"schemaVersion"`
	Manifests     []localDescriptor `json:"manifests"`
}

type localDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int               `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PrepareLocalPull exposes one repository manifest as a transient OCI layout.
// Repository admission and its mutation lock remain held until Close so cleanup
// or deletion cannot remove a symlink target while containers/image imports it.
func (application *Application) PrepareLocalPull(ctx context.Context, generatedRoot, repositoryName, reference string) (LocalPull, error) {
	if !filepath.IsAbs(generatedRoot) || filepath.Clean(generatedRoot) != generatedRoot || generatedRoot == string(filepath.Separator) {
		return LocalPull{}, fmt.Errorf("%w: generated root is invalid", ErrInvalidInput)
	}
	repository, err := application.store.RegistryRepositoryByName(ctx, repositoryName)
	if err != nil {
		return LocalPull{}, err
	}
	finishRequest, err := application.BeginRepositoryRequest(repository.ID)
	if err != nil {
		return LocalPull{}, err
	}
	lock := application.acquireRepositoryLock(repository.ID)
	var closeOnce sync.Once
	closeResources := func() {
		closeOnce.Do(func() {
			application.releaseRepositoryLock(repository.ID, lock)
			finishRequest()
		})
	}
	manifest, err := application.store.RegistryManifest(ctx, repository.ID, reference)
	if err != nil {
		closeResources()
		return LocalPull{}, err
	}
	root := filepath.Join(generatedRoot, "registry-pulls")
	if err := os.MkdirAll(root, 0o700); err != nil {
		closeResources()
		return LocalPull{}, err
	}
	layout, err := os.MkdirTemp(root, "pull-")
	if err != nil {
		closeResources()
		return LocalPull{}, err
	}
	cleanup := sync.OnceFunc(func() {
		// This directory is derived runtime state and is reset on every startup.
		// A failed best-effort removal must never leave the repository locked.
		_ = os.RemoveAll(layout)
		closeResources()
	})
	if err := application.writeLocalLayout(ctx, layout, manifest); err != nil {
		cleanup()
		return LocalPull{}, err
	}
	return LocalPull{
		Reference: "oci:" + layout, Digest: manifest.Digest, Close: cleanup,
	}, nil
}

func (application *Application) writeLocalLayout(ctx context.Context, layout string, root state.RegistryManifest) error {
	blobs := filepath.Join(layout, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layout, "oci-layout"), []byte("{\"imageLayoutVersion\":\"1.0.0\"}"), 0o600); err != nil {
		return err
	}
	indexBody, err := json.Marshal(localIndex{SchemaVersion: 2, Manifests: []localDescriptor{{
		MediaType: root.MediaType, Digest: root.Digest, Size: len(root.Body),
		Annotations: map[string]string{ociImageReferenceAnnotation: "platformd"},
	}}})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layout, "index.json"), indexBody, 0o600); err != nil {
		return err
	}
	seenManifests := make(map[string]struct{})
	seenBlobs := make(map[string]struct{})
	return application.writeLocalManifest(ctx, layout, root, seenManifests, seenBlobs)
}

func (application *Application) writeLocalManifest(ctx context.Context, layout string, manifest state.RegistryManifest, seenManifests, seenBlobs map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := seenManifests[manifest.Digest]; exists {
		return nil
	}
	seenManifests[manifest.Digest] = struct{}{}
	path := filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(manifest.Digest, "sha256:"))
	if err := os.WriteFile(path, manifest.Body, 0o600); err != nil {
		return err
	}
	var document manifestDocument
	if err := json.Unmarshal(manifest.Body, &document); err != nil {
		return fmt.Errorf("decode stored manifest for local pull: %w", err)
	}
	if manifest.MediaType == OCIImageIndexMediaType || manifest.MediaType == DockerManifestListMediaType {
		for _, descriptor := range document.Manifests {
			child, err := application.store.RegistryManifest(ctx, manifest.RepositoryID, descriptor.Digest)
			if err != nil {
				return err
			}
			if err := application.writeLocalManifest(ctx, layout, child, seenManifests, seenBlobs); err != nil {
				return err
			}
		}
		return nil
	}
	for _, descriptor := range append([]manifestDescriptor{document.Config}, document.Layers...) {
		if _, exists := seenBlobs[descriptor.Digest]; exists {
			continue
		}
		seenBlobs[descriptor.Digest] = struct{}{}
		source, err := application.payloads.blobPath(manifest.RepositoryID, descriptor.Digest)
		if err != nil {
			return err
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("registry blob for local pull is not a regular file")
		}
		destination := filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(descriptor.Digest, "sha256:"))
		if err := os.Symlink(source, destination); err != nil {
			return err
		}
	}
	return nil
}
