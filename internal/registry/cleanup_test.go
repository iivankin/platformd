package registry

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRegistryCleanupPreviewAndExecutionPreserveReferencedAndRecentBlobs(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	authentication, err := fixture.application.Authenticate(
		ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	referenced := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("referenced"))
	orphan := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("old orphan"))
	recent := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("recent orphan"))
	for digest, age := range map[string]time.Duration{
		referenced: RegistryOrphanBlobGrace + time.Hour,
		orphan:     RegistryOrphanBlobGrace + time.Hour,
		recent:     time.Hour,
	} {
		path, err := fixture.application.payloads.blobPath(fixture.private.Repository.ID, digest)
		if err != nil {
			t.Fatal(err)
		}
		modified := fixture.application.now().Add(-age)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte(fmt.Sprintf(`{
  "schemaVersion":2,
  "mediaType":%q,
  "config":{"digest":%q},
  "layers":[]
}`, OCIImageManifestMediaType, referenced))
	if _, err := fixture.application.PutManifest(ctx, authentication, "latest", OCIImageManifestMediaType, body); err != nil {
		t.Fatal(err)
	}
	actor := Actor{Kind: "access", ID: "user", Email: "admin@example.com"}
	preview, err := fixture.application.Cleanup(ctx, fixture.private.Repository.ID, true, actor)
	if err != nil || preview.Deleted || preview.BlobCount != 1 || len(preview.PreviewDigests) != 1 || preview.PreviewDigests[0] != orphan {
		t.Fatalf("cleanup preview = %+v, %v", preview, err)
	}
	result, err := fixture.application.Cleanup(ctx, fixture.private.Repository.ID, false, actor)
	if err != nil || !result.Deleted || result.RequestID == "" || result.BlobCount != 1 {
		t.Fatalf("cleanup result = %+v, %v", result, err)
	}
	for digest, expected := range map[string]bool{referenced: true, orphan: false, recent: true} {
		exists, err := fixture.application.payloads.BlobExists(fixture.private.Repository.ID, digest)
		if err != nil || exists != expected {
			t.Fatalf("blob %s exists=%t, want %t: %v", digest, exists, expected, err)
		}
	}
}
