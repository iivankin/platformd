package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/state"
)

func TestBuildControlEncryptsConsistentSQLiteAndExactRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := state.Open(ctx, filepath.Join(root, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "audit", ActorID: "user",
		ActorEmail: "user@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	paths, publicKey := controlReleaseSlot(t, root)
	master := cryptobox.MasterKey{1, 2, 3, 4}
	built, err := BuildControl(ctx, ControlBuildConfig{
		Store: store, Master: master, InstallationID: "installation", GenerationID: "generation",
		ReleaseSlot: filepath.Join(paths.ReleasesRoot, "1.2.3"), WorkRoot: filepath.Join(root, "work"),
		ExpectedUID: os.Geteuid(), PublicKey: publicKey, CreatedAt: time.Unix(10, 0),
		Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, 24*32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	if built.Manifest.PlatformVersion != "1.2.3" || built.Manifest.SchemaVersion != state.SupportedSchemaVersion() ||
		len(built.Manifest.Resources.RegistryRepositories) != 0 || len(built.Envelope.Chunks) == 0 {
		t.Fatalf("control build metadata = %+v / %+v", built.Manifest, built.Envelope)
	}
	if _, err := os.Lstat(filepath.Join(built.WorkDirectory, "snapshot.db")); !os.IsNotExist(err) {
		t.Fatalf("plaintext snapshot survived build: %v", err)
	}
	cipher, err := backupcrypto.NewControlCipher(master, "installation")
	if err != nil {
		t.Fatal(err)
	}
	var plaintext bytes.Buffer
	for _, chunk := range built.Chunks {
		sealed, err := os.ReadFile(chunk.Path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, []byte("CREATE TABLE projects")) {
			t.Fatal("SQLite plaintext appeared in encrypted work chunk")
		}
		opened, err := cipher.OpenChunk("generation", chunk.Chunk, sealed)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = plaintext.Write(opened)
		clear(opened)
	}
	archive := tar.NewReader(&plaintext)
	wantNames := []string{controlManifestName, controlDatabaseName, releaseManifestName, releaseBinaryName}
	for _, want := range wantNames {
		header, err := archive.Next()
		if err != nil || header.Name != want {
			t.Fatalf("archive entry = %+v, %v; want %s", header, err, want)
		}
		content, err := io.ReadAll(archive)
		if err != nil || int64(len(content)) != header.Size {
			t.Fatalf("archive content %s = %d/%d, %v", want, len(content), header.Size, err)
		}
		if want == controlManifestName {
			var manifest ControlManifest
			if err := json.Unmarshal(content, &manifest); err != nil || manifest.InstallationID != "installation" {
				t.Fatalf("encrypted control manifest = %+v, %v", manifest, err)
			}
		}
	}
	if _, err := archive.Next(); err != io.EOF {
		t.Fatalf("archive trailing entry = %v", err)
	}
	decoded, err := DecodeControlEnvelope(built.EnvelopeBytes)
	if err != nil || decoded.GenerationID != "generation" {
		t.Fatalf("decoded envelope = %+v, %v", decoded, err)
	}
}

func TestControlMetadataRejectsDuplicateKeysAndUnsafeIdentity(t *testing.T) {
	t.Parallel()
	completion := []byte(`{"formatVersion":1,"installationId":"installation","generationId":"generation","generationId":"other","envelopeSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","completedAt":1}`)
	if _, err := DecodeControlCompletion(completion); err == nil {
		t.Fatal("duplicate completion key was accepted")
	}
	envelope := []byte(`{"formatVersion":1,"installationId":"installation","generationId":"../escape","createdAt":1,"chunks":[{"index":0,"plaintextSize":1,"ciphertextSize":17,"ciphertextSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	if _, err := DecodeControlEnvelope(envelope); err == nil {
		t.Fatal("unsafe generation ID was accepted")
	}
}

func controlReleaseSlot(t *testing.T, root string) (layout.Paths, ed25519.PublicKey) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "platformd")
	if err := os.WriteFile(executable, []byte("\x7fELF-control-backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(directory, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeControlRuntimeProfile(t, runtimeRoot)
	if err := releasebundle.Append(executable, runtimeRoot); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture: "amd64", BinarySHA256: hex.EncodeToString(hash[:]), BinarySize: int64(len(binary)),
		BinaryURL: "https://example.com/platformd", Format: 1, OS: "linux", Version: "1.2.3",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	paths := layout.FromRoots(filepath.Join(root, "data"), filepath.Join(root, "config"), filepath.Join(root, "run"), filepath.Join(root, "bin", "platformd"), filepath.Join(root, "platformd.service"))
	if err := bootstrap.PublishReleaseSlot(bootstrap.VerifiedRelease{
		ExecutablePath: executable, Manifest: manifest, ManifestBytes: manifestBytes, PublicKey: publicKey,
	}, paths, os.Geteuid()); err != nil {
		t.Fatal(err)
	}
	return paths, publicKey
}

func writeControlRuntimeProfile(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"catatonit", "conmon", "crun", "netavark"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"containers.conf", "mounts.conf", "policy.json", "registries.conf", "seccomp.json", "storage.conf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
