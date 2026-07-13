package registry

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestRestoreSnapshotAtomicallyReplacesCatalogAndUsesExplicitPolicyMode(t *testing.T) {
	for _, test := range []struct {
		name           string
		mode           RestorePolicyMode
		wantPublic     bool
		wantBackup     bool
		wantBackupCron string
	}{
		{name: "keep current policy", mode: RestoreKeepCurrentPolicy},
		{name: "apply snapshot policy", mode: RestoreApplySnapshotPolicy, wantPublic: true, wantBackup: true, wantBackupCron: "0 */6 * * *"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRegistryHTTPFixture(t)
			ctx := context.Background()
			repositoryID := fixture.private.Repository.ID
			store := fixture.application.store.(*state.Store)

			authentication, err := fixture.application.Authenticate(
				ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
			)
			if err != nil {
				t.Fatal(err)
			}
			oldBlob := uploadTestBlob(t, ctx, fixture.application, authentication, []byte("old payload"))
			oldBody := []byte(fmt.Sprintf(
				`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[]}`,
				OCIImageManifestMediaType, oldBlob,
			))
			oldManifest, err := fixture.application.PutManifest(
				ctx, authentication, "old", OCIImageManifestMediaType, oldBody,
			)
			if err != nil {
				t.Fatal(err)
			}
			upload, err := fixture.application.BeginUpload(ctx, authentication)
			if err != nil {
				t.Fatal(err)
			}

			newPayload := []byte("restored payload")
			newBlob := digestForTest(newPayload)
			newBody := []byte(fmt.Sprintf(
				`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[]}`,
				OCIImageManifestMediaType, newBlob,
			))
			newManifest := digestForTest(newBody)
			snapshot := BackupSnapshot{
				FormatVersion: BackupFormatVersion,
				Repository: BackupRepository{
					ID: repositoryID, Name: fixture.private.Repository.Name, PublicPull: true,
					BackupEnabled: true, BackupCron: "0 */6 * * *", BackupRetentionCount: 11,
				},
				Manifests: []BackupManifest{{
					Digest: newManifest, MediaType: OCIImageManifestMediaType,
					Body: newBody, PushedAtMillis: 100,
				}},
				Tags:  []BackupTag{{Name: "latest", ManifestDigest: newManifest, UpdatedAtMillis: 101}},
				Blobs: []BackupBlob{{Digest: newBlob, Size: int64(len(newPayload))}},
			}
			archive := encodeRegistryBackupForTest(
				t, snapshot, map[string][]byte{newBlob: newPayload}, "",
			)
			requestID, err := fixture.application.RestoreSnapshot(ctx, RestoreInput{
				RepositoryID: repositoryID, Archive: bytes.NewReader(archive), PolicyMode: test.mode,
				Actor: Actor{Kind: "system", ID: "disaster_restore"},
			})
			if err != nil || requestID == "" {
				t.Fatalf("restore request = %q, %v", requestID, err)
			}

			if _, err := fixture.application.Manifest(ctx, repositoryID, oldManifest.Digest); !errors.Is(err, state.ErrRegistryManifestNotFound) {
				t.Fatalf("old manifest remains after restore: %v", err)
			}
			restored, err := fixture.application.Manifest(ctx, repositoryID, "latest")
			if err != nil || restored.Digest != newManifest || !bytes.Equal(restored.Body, newBody) {
				t.Fatalf("restored manifest = %+v, %v", restored, err)
			}
			if _, err := store.RegistryUpload(ctx, upload.ID); !errors.Is(err, state.ErrRegistryUploadNotFound) {
				t.Fatalf("pre-restore upload remains: %v", err)
			}
			if _, err := fixture.application.payloads.UploadSize(repositoryID, upload.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pre-restore upload payload remains: %v", err)
			}
			if _, err := fixture.application.Authenticate(
				ctx, fixture.private.Repository.Name, fixture.private.Username, fixture.private.Secret, true,
			); err != nil {
				t.Fatalf("current robot credential did not survive restore: %v", err)
			}
			repository, err := store.RegistryRepository(ctx, repositoryID)
			if err != nil || repository.PublicPull != test.wantPublic || repository.BackupEnabled != test.wantBackup ||
				repository.BackupCron != test.wantBackupCron {
				t.Fatalf("repository policy after restore = %+v, %v", repository, err)
			}
			wantRetention := 7
			if test.mode == RestoreApplySnapshotPolicy {
				wantRetention = 11
			}
			if repository.BackupRetentionCount != wantRetention {
				t.Fatalf("backup retention = %d, want %d", repository.BackupRetentionCount, wantRetention)
			}
			audit, err := store.AuditEvents(ctx, state.AuditQuery{Action: "registry.restore", Limit: 10})
			if err != nil || len(audit.Events) != 1 || audit.Events[0].ActorKind != "system" ||
				audit.Events[0].ActorID != "disaster_restore" {
				t.Fatalf("restore audit = %+v, %v", audit, err)
			}
		})
	}
}

func TestRestoreSnapshotRejectsRepositoryIdentityMismatchBeforePublication(t *testing.T) {
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	repositoryID := fixture.private.Repository.ID
	payload := []byte("restored payload")
	blob := digestForTest(payload)
	body := []byte(fmt.Sprintf(
		`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[]}`,
		OCIImageManifestMediaType, blob,
	))
	snapshot := BackupSnapshot{
		FormatVersion: BackupFormatVersion,
		Repository: BackupRepository{
			ID: repositoryID, Name: "different/name", BackupRetentionCount: 7,
		},
		Manifests: []BackupManifest{{
			Digest: digestForTest(body), MediaType: OCIImageManifestMediaType,
			Body: body, PushedAtMillis: 100,
		}},
		Blobs: []BackupBlob{{Digest: blob, Size: int64(len(payload))}},
	}
	archive := encodeRegistryBackupForTest(t, snapshot, map[string][]byte{blob: payload}, "")
	if _, err := fixture.application.RestoreSnapshot(ctx, RestoreInput{
		RepositoryID: repositoryID, Archive: bytes.NewReader(archive),
		PolicyMode: RestoreKeepCurrentPolicy,
		Actor:      Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	}); err == nil {
		t.Fatal("repository name mismatch was accepted")
	}
	stats, err := fixture.application.store.RegistryRepositoryMetadataStats(ctx, repositoryID)
	if err != nil || stats.ManifestCount != 0 || stats.TagCount != 0 {
		t.Fatalf("catalog changed after rejected restore: %+v, %v", stats, err)
	}
}

func TestRestoreBackupArchiveVerifiesAndInstallsRepositoryLocalBlobs(t *testing.T) {
	t.Parallel()
	config := []byte("config payload")
	layer := []byte("layer payload")
	configDigest := digestForTest(config)
	layerDigest := digestForTest(layer)
	manifestBody := []byte(fmt.Sprintf(
		`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[{"digest":%q}]}`,
		OCIImageManifestMediaType, configDigest, layerDigest,
	))
	manifestDigest := digestForTest(manifestBody)
	blobs := []BackupBlob{
		{Digest: configDigest, Size: int64(len(config))},
		{Digest: layerDigest, Size: int64(len(layer))},
	}
	sort.Slice(blobs, func(left, right int) bool { return blobs[left].Digest < blobs[right].Digest })
	snapshot := BackupSnapshot{
		FormatVersion: BackupFormatVersion,
		Repository: BackupRepository{
			ID: "repository-1", Name: "team/api", PublicPull: true,
			BackupEnabled: true, BackupCron: "0 */6 * * *", BackupRetentionCount: 7,
		},
		Manifests: []BackupManifest{{
			Digest: manifestDigest, MediaType: OCIImageManifestMediaType,
			Body: manifestBody, PushedAtMillis: 100,
		}},
		Tags:  []BackupTag{{Name: "latest", ManifestDigest: manifestDigest, UpdatedAtMillis: 101}},
		Blobs: blobs,
	}
	archive := encodeRegistryBackupForTest(t, snapshot, map[string][]byte{
		configDigest: config,
		layerDigest:  layer,
	}, "")
	payloads, err := NewPayloadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	restored, err := restoreBackupArchive(
		context.Background(), snapshot.Repository.ID, bytes.NewReader(archive), payloads,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Repository.Name != "team/api" || len(restored.Manifests) != 1 || len(restored.Blobs) != 2 {
		t.Fatalf("restored snapshot = %+v", restored)
	}
	for digest, expected := range map[string][]byte{configDigest: config, layerDigest: layer} {
		file, size, err := payloads.OpenBlob(snapshot.Repository.ID, digest)
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || size != int64(len(expected)) || !bytes.Equal(value, expected) {
			t.Fatalf("blob %s = %q/%d, read=%v close=%v", digest, value, size, readErr, closeErr)
		}
	}
}

func TestRestoreBackupArchiveRejectsCorruptBlobBeforePublication(t *testing.T) {
	t.Parallel()
	payload := []byte("expected")
	digest := digestForTest(payload)
	manifestBody := []byte(fmt.Sprintf(
		`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[]}`,
		OCIImageManifestMediaType, digest,
	))
	snapshot := BackupSnapshot{
		FormatVersion: BackupFormatVersion,
		Repository:    BackupRepository{ID: "repository-1", Name: "team/api", BackupRetentionCount: 7},
		Manifests: []BackupManifest{{
			Digest: digestForTest(manifestBody), MediaType: OCIImageManifestMediaType,
			Body: manifestBody, PushedAtMillis: 100,
		}},
		Blobs: []BackupBlob{{Digest: digest, Size: int64(len(payload))}},
	}
	archive := encodeRegistryBackupForTest(t, snapshot, map[string][]byte{digest: []byte("tampered")}, "")
	payloads, err := NewPayloadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoreBackupArchive(
		context.Background(), snapshot.Repository.ID, bytes.NewReader(archive), payloads,
	); err == nil {
		t.Fatal("corrupt registry backup blob was accepted")
	}
	exists, err := payloads.BlobExists(snapshot.Repository.ID, digest)
	if err != nil || exists {
		t.Fatalf("corrupt blob exists = %t, %v", exists, err)
	}
}

func TestRestoreBackupArchiveRejectsNonCanonicalMetadataAndExtraEntries(t *testing.T) {
	t.Parallel()
	payloads, err := NewPayloadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := []byte(`{"formatVersion":1,"formatVersion":1,"repository":{"id":"repository-1","name":"team/api","publicPull":false,"backupEnabled":false,"backupRetentionCount":7},"manifests":[],"tags":[],"blobs":[]}`)
	archive := encodeRawRegistryBackupForTest(t, duplicate, nil, "")
	if _, err := restoreBackupArchive(
		context.Background(), "repository-1", bytes.NewReader(archive), payloads,
	); err == nil {
		t.Fatal("duplicate registry backup JSON key was accepted")
	}

	snapshot := BackupSnapshot{
		FormatVersion: BackupFormatVersion,
		Repository:    BackupRepository{ID: "repository-1", Name: "team/api", BackupRetentionCount: 7},
	}
	metadata, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	archive = encodeRawRegistryBackupForTest(t, metadata, nil, "unexpected")
	if _, err := restoreBackupArchive(
		context.Background(), "repository-1", bytes.NewReader(archive), payloads,
	); err == nil {
		t.Fatal("unexpected registry backup tar entry was accepted")
	}
}

func encodeRegistryBackupForTest(
	t *testing.T,
	snapshot BackupSnapshot,
	blobs map[string][]byte,
	extra string,
) []byte {
	t.Helper()
	metadata, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return encodeRawRegistryBackupForTest(t, metadata, func(writer *tar.Writer) {
		for _, blob := range snapshot.Blobs {
			value := blobs[blob.Digest]
			writeRegistryBackupEntryForTest(t, writer, "blobs/sha256/"+blob.Digest[7:], value)
		}
	}, extra)
}

func encodeRawRegistryBackupForTest(
	t *testing.T,
	metadata []byte,
	blobs func(*tar.Writer),
	extra string,
) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	writeRegistryBackupEntryForTest(t, writer, "manifest.json", metadata)
	if blobs != nil {
		blobs(writer)
	}
	if extra != "" {
		writeRegistryBackupEntryForTest(t, writer, extra, []byte("extra"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeRegistryBackupEntryForTest(t *testing.T, writer *tar.Writer, name string, value []byte) {
	t.Helper()
	if err := writer.WriteHeader(&tar.Header{
		Name: name, Mode: 0o600, Size: int64(len(value)), Typeflag: tar.TypeReg, Format: tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
}

func digestForTest(value []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(value))
}
