package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreRegistryRepositoryRollsBackCatalogAndUploadsWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository, credential, err := store.CreateRegistryRepository(ctx, CreateRegistryRepository{
		ID: "repository", Name: "team/api", CredentialID: "credential",
		CredentialName: "default", CredentialPermission: "pull_push",
		CredentialSecretHMAC: make([]byte, 32), AuditEventID: "duplicate-audit",
		ActorKind: "access", ActorID: "user", ActorEmail: "admin@example.com",
		CreatedAtMillis: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.PutRegistryManifest(ctx, PutRegistryManifest{
		RepositoryID: repository.ID, Digest: oldDigest, MediaType: "application/test",
		Body: []byte("old"), Tag: "old", PushedAtMillis: 20, MaximumForRepository: 10,
	}); err != nil {
		t.Fatal(err)
	}
	upload, err := store.CreateRegistryUpload(ctx, CreateRegistryUpload{
		ID: "upload", RepositoryID: repository.ID, CredentialID: credential.ID,
		CreatedAtMillis: 20, ExpiresAtMillis: 100, MaximumForCredential: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = store.RestoreRegistryRepository(ctx, RestoreRegistryRepository{
		RepositoryID: repository.ID,
		Manifests: []RegistryManifest{{
			RepositoryID: repository.ID, Digest: newDigest, MediaType: "application/test",
			Body: []byte("new"), PushedAtMillis: 30,
		}},
		Tags:                 []RegistryTag{{Name: "latest", ManifestDigest: newDigest, UpdatedAtMillis: 30}},
		BackupRetentionCount: 7, AuditEventID: "duplicate-audit",
		ActorKind: "system", ActorID: "disaster_restore", CreatedAtMillis: 30,
	})
	if err == nil {
		t.Fatal("duplicate audit did not fail registry restore")
	}
	if _, err := store.RegistryManifest(ctx, repository.ID, oldDigest); err != nil {
		t.Fatalf("old manifest was not rolled back: %v", err)
	}
	if _, err := store.RegistryManifest(ctx, repository.ID, newDigest); !errors.Is(err, ErrRegistryManifestNotFound) {
		t.Fatalf("new manifest survived failed restore: %v", err)
	}
	if _, err := store.RegistryUpload(ctx, upload.ID); err != nil {
		t.Fatalf("upload deletion was not rolled back: %v", err)
	}
	tags, more, err := store.RegistryTags(ctx, repository.ID, "", 10)
	if err != nil || more || len(tags) != 1 || tags[0].Name != "old" {
		t.Fatalf("tags after failed restore = %+v, more=%t, err=%v", tags, more, err)
	}
}
