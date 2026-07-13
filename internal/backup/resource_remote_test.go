package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestResourcePublicationAndStreamingRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{1, 2, 3, 4}
	payload := bytes.Repeat([]byte("resource payload\n"), backupcrypto.DefaultChunkSize/17+100)
	built := resourcePublicationBuild(t, master, "postgres", "database-1", "generation-1", payload, time.Unix(20, 0))
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()

	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	generations, err := ListResourceGenerations(ctx, remote, "postgres", "database-1")
	if err != nil || len(generations) != 1 {
		t.Fatalf("generations = %+v, %v", generations, err)
	}
	reader, err := OpenResource(ctx, remote, master, generations[0])
	if err != nil {
		t.Fatal(err)
	}
	restored, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatal("restored resource differs from source")
	}
	expectedRemoteSize := int64(len(built.EnvelopeBytes))
	for _, chunk := range built.Chunks {
		expectedRemoteSize += int64(chunk.CiphertextSize)
	}
	if generations[0].RemoteSize != expectedRemoteSize {
		t.Fatalf("remote size = %d, want %d", generations[0].RemoteSize, expectedRemoteSize)
	}
}

func TestPublishResourceDoesNotCompleteCorruptReadback(t *testing.T) {
	t.Parallel()
	master := cryptobox.MasterKey{5, 6, 7, 8}
	built := resourcePublicationBuild(t, master, "redis", "redis-1", "generation-1", []byte("rdb"), time.Unix(30, 0))
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()
	remote.corruptOnRead = "chunk-"

	if err := PublishResource(context.Background(), remote, master, built); err == nil {
		t.Fatal("corrupt resource read-back was accepted")
	}
	completionKey := remote.Key(ResourceCompletionKey("redis", "redis-1", "generation-1"))
	if _, exists := remote.objects[completionKey]; exists {
		t.Fatal("resource completion was published before successful verification")
	}
}

func TestResourceGenerationsAreNewestFirstAndRetentionKeepsIncompletePrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{9, 10, 11, 12}
	remote := newMemoryControlRemote()
	for index, generation := range []string{"generation-1", "generation-2", "generation-3"} {
		built := resourcePublicationBuild(
			t, master, "registry", "registry-1", generation, []byte(generation), time.Unix(int64(40+index), 0),
		)
		if err := PublishResource(ctx, remote, master, built); err != nil {
			os.RemoveAll(built.WorkDirectory)
			t.Fatal(err)
		}
		os.RemoveAll(built.WorkDirectory)
	}
	incompleteKey := remote.Key(ResourceChunkKey("registry", "registry-1", "incomplete", 0))
	remote.objects[incompleteKey] = []byte("partial")

	generations, err := ListResourceGenerations(ctx, remote, "registry", "registry-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 3 || generations[0].GenerationID != "generation-3" || generations[2].GenerationID != "generation-1" {
		t.Fatalf("generation order = %+v", generations)
	}
	if err := ApplyResourceRetention(ctx, remote, "registry", "registry-1", 2); err != nil {
		t.Fatal(err)
	}
	if _, exists := remote.objects[incompleteKey]; !exists {
		t.Fatal("retention deleted an incomplete generation")
	}
	for key := range remote.objects {
		if strings.Contains(key, "/generation-1/") {
			t.Fatalf("old complete generation survived retention: %s", key)
		}
	}
}

func TestOpenResourceRejectsInvalidCompletionBeforeRemoteRead(t *testing.T) {
	t.Parallel()
	remote := newMemoryControlRemote()
	completion := ResourceCompletion{
		FormatVersion: ControlFormatVersion, ResourceKind: "postgres", ResourceID: "../database",
		GenerationID: "generation", EnvelopeSHA256: strings.Repeat("a", 64), PlaintextSize: 1,
		RemoteSize: 1, CompletedAtMillis: 1,
	}

	if _, err := OpenResource(context.Background(), remote, cryptobox.MasterKey{1}, completion); err == nil {
		t.Fatal("invalid completion identity was accepted")
	}
}

func TestResourceRestoreRejectsDifferentMasterKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{13, 14, 15, 16}
	built := resourcePublicationBuild(t, master, "object_store", "store-1", "generation-1", []byte("encrypted"), time.Unix(60, 0))
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()
	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenResource(ctx, remote, cryptobox.MasterKey{99}, built.Completion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("resource decrypted with a different master key")
	}
}

func TestResourceAttachmentsPublishCiphertextByteForByteAndVerifyCommitment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{21, 22, 23, 24}
	root := t.TempDir()
	attachmentPayload := bytes.Repeat([]byte{0x91, 0x42, 0x17}, 4096)
	attachmentPath := filepath.Join(root, "payload.chunk")
	if err := os.WriteFile(attachmentPath, attachmentPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	built, err := BuildResource(ctx, ResourceBuildConfig{
		Master: master, ResourceKind: "object_store", ResourceID: "store-1", GenerationID: "generation-1",
		WorkRoot: filepath.Join(root, "work"), CreatedAt: time.Unix(70, 0),
		Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, 24*4)), AttachmentPaths: []string{attachmentPath},
	}, bytes.NewReader([]byte(`{"objects":[{"payload":"payload-1","attachment":0}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()
	if err := PublishResource(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	attachmentKey := remote.Key(ResourceAttachmentKey("object_store", "store-1", "generation-1", 0))
	if !bytes.Equal(remote.objects[attachmentKey], attachmentPayload) {
		t.Fatal("resource attachment was re-encrypted or otherwise changed")
	}
	reader, err := OpenResource(ctx, remote, master, built.Completion)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := io.ReadAll(reader)
	if err != nil || !bytes.Contains(metadata, []byte("payload-1")) {
		t.Fatalf("resource metadata = %q, %v", metadata, err)
	}
	descriptors := []ResourceAttachment{built.Attachments[0].ResourceAttachment}
	if err := ValidateResourceAttachments(reader.Envelope(), descriptors); err != nil {
		t.Fatal(err)
	}
	attachment, err := OpenResourceAttachment(ctx, remote, reader.Envelope(), descriptors[0])
	if err != nil {
		t.Fatal(err)
	}
	restored, readErr := io.ReadAll(attachment)
	closeErr := attachment.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(restored, attachmentPayload) {
		t.Fatalf("restored attachment = %d bytes, read=%v close=%v", len(restored), readErr, closeErr)
	}
}

func TestPublishResourceDoesNotCompleteCorruptAttachmentReadback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	attachmentPath := filepath.Join(root, "payload.chunk")
	if err := os.WriteFile(attachmentPath, []byte("already-encrypted-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{31, 32, 33, 34}
	built, err := BuildResource(ctx, ResourceBuildConfig{
		Master: master, ResourceKind: "object_store", ResourceID: "store-1", GenerationID: "generation-1",
		WorkRoot: filepath.Join(root, "work"), CreatedAt: time.Unix(80, 0),
		Random: bytes.NewReader(bytes.Repeat([]byte{0x66}, 24*4)), AttachmentPaths: []string{attachmentPath},
	}, bytes.NewReader([]byte(`{"objects":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()
	remote.corruptOnRead = "attachment-"
	if err := PublishResource(ctx, remote, master, built); err == nil {
		t.Fatal("corrupt attachment read-back was accepted")
	}
	completionKey := remote.Key(ResourceCompletionKey("object_store", "store-1", "generation-1"))
	if _, exists := remote.objects[completionKey]; exists {
		t.Fatal("completion was published before attachment verification")
	}
}

func resourcePublicationBuild(
	t *testing.T,
	master cryptobox.MasterKey,
	kind, resourceID, generationID string,
	payload []byte,
	createdAt time.Time,
) ResourceBuild {
	t.Helper()
	// One nonce per possible chunk; the deterministic reader keeps tests stable.
	random := bytes.NewReader(bytes.Repeat([]byte{0x44}, 24*(len(payload)/backupcrypto.DefaultChunkSize+2)))
	built, err := BuildResource(context.Background(), ResourceBuildConfig{
		Master: master, ResourceKind: kind, ResourceID: resourceID, GenerationID: generationID,
		WorkRoot: t.TempDir(), CreatedAt: createdAt, Random: random,
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	return built
}
