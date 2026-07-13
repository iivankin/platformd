package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProtectedManifestAndRepositoryDeletion(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	authentication, err := fixture.application.Authenticate(
		ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	blobDigest := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("image payload"))
	childBody := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[]}`,
		OCIImageManifestMediaType, blobDigest))
	child, err := fixture.application.PutManifest(ctx, authentication, "linux", OCIImageManifestMediaType, childBody)
	if err != nil {
		t.Fatal(err)
	}
	indexBody := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[{"digest":%q,"platform":{"os":"linux","architecture":"amd64"}}]}`,
		OCIImageIndexMediaType, child.Digest))
	index, err := fixture.application.PutManifest(ctx, authentication, "latest", OCIImageIndexMediaType, indexBody)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{Kind: "access", ID: "user", Email: "admin@example.com"}
	if _, _, err := fixture.application.DeleteManifest(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, Reference: child.Digest, Actor: actor,
	}); err == nil {
		t.Fatal("referenced child manifest was deleted")
	} else {
		var referenced *ManifestReferencedError
		if !errors.As(err, &referenced) || len(referenced.Parents) != 1 || referenced.Parents[0] != index.Digest {
			t.Fatalf("child deletion error = %v", err)
		}
	}
	if digest, _, err := fixture.application.DeleteTag(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, Reference: "latest", Actor: actor,
	}); err != nil || digest != index.Digest {
		t.Fatalf("delete tag = %q, %v", digest, err)
	}
	if tags, _, err := fixture.application.DeleteManifest(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, Reference: index.Digest, Actor: actor,
	}); err != nil || len(tags) != 0 {
		t.Fatalf("delete index = %v, %v", tags, err)
	}
	if tags, _, err := fixture.application.DeleteManifest(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, Reference: child.Digest, Actor: actor,
	}); err != nil || len(tags) != 1 || tags[0] != "linux" {
		t.Fatalf("delete child = %v, %v", tags, err)
	}

	payloadPath := filepath.Join(fixture.application.payloads.root, fixture.private.Repository.ID)
	if _, err := os.Stat(payloadPath); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.DeleteRepository(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, ExpectedName: fixture.private.Repository.Name, Actor: actor,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(payloadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository payload directory survived: %v", err)
	}
	if _, err := fixture.application.BeginRepositoryRequest(fixture.private.Repository.ID); !errors.Is(err, ErrRepositoryBusy) {
		t.Fatalf("stale request admission error = %v", err)
	}
}

func TestRepositoryDrainTimeoutReopensAdmission(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	finish, err := fixture.application.BeginRepositoryRequest(fixture.private.Repository.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = fixture.application.DeleteRepository(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, ExpectedName: fixture.private.Repository.Name,
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if !errors.Is(err, ErrRepositoryBusy) {
		t.Fatalf("drain timeout error = %v", err)
	}
	finish()
	second, err := fixture.application.BeginRepositoryRequest(fixture.private.Repository.ID)
	if err != nil {
		t.Fatalf("admission remained blocked after timeout: %v", err)
	}
	second()
}
