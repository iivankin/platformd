package registry

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestBackupSnapshotStreamsStableMetadataAndOnlyReferencedBlobs(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	authentication, err := fixture.application.Authenticate(
		ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	configDigest := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("config payload"))
	layerDigest := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("layer payload"))
	orphanDigest := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("orphan payload"))
	body := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[{"digest":%q}]}`,
		OCIImageManifestMediaType, configDigest, layerDigest))
	if _, err := fixture.application.PutManifest(ctx, authentication, "latest", OCIImageManifestMediaType, body); err != nil {
		t.Fatal(err)
	}
	export, err := fixture.application.BackupSnapshot(ctx, fixture.private.Repository.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer export.Release()
	actor := Actor{Kind: "access", ID: "user", Email: "admin@example.com"}
	if _, err := fixture.application.Cleanup(ctx, fixture.private.Repository.ID, true, actor); !errors.Is(err, ErrRepositoryBusy) {
		t.Fatalf("cleanup during backup error = %v", err)
	}
	if _, err := fixture.application.DeleteRepository(ctx, DeleteInput{
		RepositoryID: fixture.private.Repository.ID, ExpectedName: fixture.private.Repository.Name, Actor: actor,
	}); !errors.Is(err, ErrRepositoryBusy) {
		t.Fatalf("delete during backup error = %v", err)
	}
	// Enumeration already ended, so ordinary manifest publication may proceed
	// while the immutable blobs are still being streamed.
	if _, err := fixture.application.PutManifest(ctx, authentication, "after-snapshot", OCIImageManifestMediaType, body); err != nil {
		t.Fatalf("manifest publication remained blocked after enumeration: %v", err)
	}
	archive, err := io.ReadAll(export.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := export.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	entries := readRegistryBackupTar(t, archive)
	var snapshot BackupSnapshot
	if err := json.Unmarshal(entries["manifest.json"], &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.FormatVersion != BackupFormatVersion || snapshot.Repository.ID != fixture.private.Repository.ID ||
		len(snapshot.Manifests) != 1 || len(snapshot.Tags) != 1 || len(snapshot.Blobs) != 2 {
		t.Fatalf("registry backup snapshot = %+v", snapshot)
	}
	if _, exists := entries["blobs/sha256/"+strings.TrimPrefix(orphanDigest, "sha256:")]; exists {
		t.Fatal("unreferenced registry blob was included in backup")
	}
	for digest, payload := range map[string][]byte{configDigest: []byte("config payload"), layerDigest: []byte("layer payload")} {
		name := "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
		if !bytes.Equal(entries[name], payload) {
			t.Fatalf("backup blob %s = %q", digest, entries[name])
		}
	}
}

func readRegistryBackupTar(t *testing.T, value []byte) map[string][]byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(value))
	result := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = body
	}
}
