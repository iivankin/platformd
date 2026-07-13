package registry

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"testing"
)

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
