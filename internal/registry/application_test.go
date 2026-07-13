package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type publisherStub struct {
	repository string
	tag        string
}

func (publisher *publisherStub) RegistryTagPublished(repository, tag string) {
	publisher.repository = repository
	publisher.tag = tag
}

func TestRepositoryLocalBlobUploadAuthenticationAndManifestPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := filepath.Join(t.TempDir(), "registry")
	payloads, err := NewPayloadStore(root)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &publisherStub{}
	application, err := NewApplication(
		store, payloads, cryptobox.MasterKey{1, 2, 3}, publisher, nil,
		func() time.Time { return time.UnixMilli(1_720_000_000_000) },
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.CreateRepository(ctx, CreateRepositoryInput{
		Name: "team/api", Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedRepository, requestID, err := application.SetPublicPull(ctx, SetPublicPullInput{
		RepositoryID: created.Repository.ID, PublicPull: true,
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil || requestID == "" || !updatedRepository.PublicPull {
		t.Fatalf("set public pull = %+v, %q, %v", updatedRepository, requestID, err)
	}
	authentication, err := application.Authenticate(ctx, created.Repository.Name, created.Username, created.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := application.MarkCredentialUsed(ctx, authentication.Credential.ID); err != nil {
		t.Fatal(err)
	}
	used, err := store.RegistryCredential(ctx, authentication.Credential.ID)
	if err != nil || used.LastUsedAtMillis != 1_720_000_000_000 {
		t.Fatalf("credential last used = %+v, %v", used, err)
	}
	if _, err := application.Authenticate(ctx, created.Repository.Name, created.Username, "wrong", true); err != ErrAuthentication {
		t.Fatalf("wrong secret error = %v", err)
	}

	configDigest := uploadTestBlob(t, ctx, application, authentication, []byte("config"))
	layerDigest := uploadTestBlob(t, ctx, application, authentication, []byte("layer payload"))
	manifestBody := []byte(fmt.Sprintf(`{
  "schemaVersion":2,
  "mediaType":%q,
  "config":{"digest":%q},
  "layers":[{"digest":%q}]
}`, OCIImageManifestMediaType, configDigest, layerDigest))
	manifest, err := application.PutManifest(ctx, authentication, "latest", OCIImageManifestMediaType, manifestBody)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.repository != "team/api" || publisher.tag != "latest" {
		t.Fatalf("publication callback = %q:%q", publisher.repository, publisher.tag)
	}
	loaded, err := application.Manifest(ctx, created.Repository.ID, "latest")
	if err != nil || loaded.Digest != manifest.Digest || !bytes.Equal(loaded.Body, manifestBody) {
		t.Fatalf("manifest = %+v, %v", loaded, err)
	}
	tags, more, err := application.Tags(ctx, created.Repository.ID, "", 100)
	if err != nil || more || len(tags) != 1 || tags[0].Name != "latest" {
		t.Fatalf("tags = %+v more=%v err=%v", tags, more, err)
	}
	local, err := application.PrepareLocalPull(ctx, filepath.Join(t.TempDir(), "generated"), created.Repository.Name, "latest")
	if err != nil || local.Digest != manifest.Digest || !strings.HasPrefix(local.Reference, "oci:") {
		t.Fatalf("local pull = %+v, %v", local, err)
	}
	layout := strings.TrimPrefix(local.Reference, "oci:")
	if _, err := os.Stat(filepath.Join(layout, "index.json")); err != nil {
		t.Fatal(err)
	}
	linkedLayer := filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(layerDigest, "sha256:"))
	if info, err := os.Lstat(linkedLayer); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("local layer link = %v, %v", info, err)
	}
	local.Close()
	if _, err := os.Stat(layout); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local layout remains after close: %v", err)
	}
	file, size, err := application.OpenBlob(created.Repository.ID, layerDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || size != int64(len(data)) || string(data) != "layer payload" {
		t.Fatalf("blob size/data = %d/%q, %v", size, data, err)
	}

	other, err := application.CreateRepository(ctx, CreateRepositoryInput{
		Name: "team/public", PublicPull: true,
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAuth, err := application.Authenticate(ctx, other.Repository.Name, other.Username, other.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.PutManifest(ctx, otherAuth, "latest", OCIImageManifestMediaType, manifestBody); err == nil {
		t.Fatal("cross-repository blob references were accepted")
	}
}

func uploadTestBlob(t *testing.T, ctx context.Context, application *Application, authentication Authentication, data []byte) string {
	t.Helper()
	upload, err := application.BeginUpload(ctx, authentication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.AppendUpload(ctx, authentication, upload.ID, bytes.NewReader(data[:len(data)/2])); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	size, err := application.FinalizeUpload(ctx, authentication, upload.ID, digest, bytes.NewReader(data[len(data)/2:]))
	if err != nil || size != int64(len(data)) {
		t.Fatalf("finalize blob = %d, %v", size, err)
	}
	return digest
}
