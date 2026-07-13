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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type memoryControlRemote struct {
	objects       map[string][]byte
	corruptOnRead string
}

func newMemoryControlRemote() *memoryControlRemote {
	return &memoryControlRemote{objects: make(map[string][]byte)}
}

func (*memoryControlRemote) Key(relative string) string { return "installation/" + relative }

func (remote *memoryControlRemote) Put(_ context.Context, key string, input io.Reader, size int64, checksum string) error {
	value, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(value)
	if int64(len(value)) != size || hex.EncodeToString(hash[:]) != checksum {
		return io.ErrUnexpectedEOF
	}
	remote.objects[key] = append([]byte(nil), value...)
	return nil
}

func (remote *memoryControlRemote) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	value, exists := remote.objects[key]
	if !exists {
		return nil, 0, &remotes3.RemoteError{StatusCode: 404, Code: "NoSuchKey"}
	}
	result := append([]byte(nil), value...)
	if remote.corruptOnRead != "" && strings.Contains(key, remote.corruptOnRead) && len(result) != 0 {
		result[len(result)-1] ^= 0xff
	}
	return io.NopCloser(bytes.NewReader(result)), int64(len(result)), nil
}

func (remote *memoryControlRemote) Delete(_ context.Context, key string) error {
	delete(remote.objects, key)
	return nil
}

func (remote *memoryControlRemote) List(_ context.Context, prefix, _ string) (remotes3.Page, error) {
	keys := make([]string, 0)
	for key := range remote.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	page := remotes3.Page{Objects: make([]remotes3.Object, 0, len(keys))}
	for _, key := range keys {
		page.Objects = append(page.Objects, remotes3.Object{Key: key, Size: int64(len(remote.objects[key]))})
	}
	return page, nil
}

func TestPublishControlVerifiesBeforeCompletionAndDeletesPrevious(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	master := cryptobox.MasterKey{1, 2, 3}
	remote := newMemoryControlRemote()

	first := controlPublicationBuild(t, master, "first", []byte("first payload"))
	defer os.RemoveAll(first.WorkDirectory)
	if err := PublishControl(ctx, remote, master, first); err != nil {
		t.Fatal(err)
	}
	second := controlPublicationBuild(t, master, "second", []byte("second payload"))
	defer os.RemoveAll(second.WorkDirectory)
	if err := PublishControl(ctx, remote, master, second); err != nil {
		t.Fatal(err)
	}

	completion, exists, err := currentControlCompletion(ctx, remote)
	if err != nil || !exists || completion.GenerationID != "second" {
		t.Fatalf("current completion = %+v, %v, %v", completion, exists, err)
	}
	for key := range remote.objects {
		if strings.Contains(key, "/first/") {
			t.Fatalf("previous generation object survived publication: %s", key)
		}
	}
}

func TestPublishControlDoesNotWriteCompletionWhenReadbackIsCorrupt(t *testing.T) {
	t.Parallel()
	master := cryptobox.MasterKey{4, 5, 6}
	remote := newMemoryControlRemote()
	remote.corruptOnRead = "chunk-"
	built := controlPublicationBuild(t, master, "corrupt", []byte("payload"))
	defer os.RemoveAll(built.WorkDirectory)

	if err := PublishControl(context.Background(), remote, master, built); err == nil {
		t.Fatal("corrupt read-back was accepted")
	}
	if _, exists := remote.objects[remote.Key(ControlCompletionKey())]; exists {
		t.Fatal("completion marker was published before successful verification")
	}
}

func TestFetchControlStreamsAndVerifiesCompleteBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, err := state.Open(ctx, filepath.Join(root, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	paths, publicKey := controlReleaseSlot(t, root)
	master := cryptobox.MasterKey{7, 8, 9}
	built, err := BuildControl(ctx, ControlBuildConfig{
		Store: store, Master: master, InstallationID: "installation", GenerationID: "generation",
		ReleaseSlot: filepath.Join(paths.ReleasesRoot, "1.2.3"), WorkRoot: filepath.Join(root, "build"),
		ExpectedUID: os.Geteuid(), PublicKey: publicKey, CreatedAt: time.Unix(20, 0),
		Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, 24*64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote := newMemoryControlRemote()
	if err := PublishControl(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	fetched, err := FetchControl(ctx, ControlFetchConfig{
		Remote: remote, Master: master, WorkRoot: filepath.Join(root, "restore"), ExpectedUID: os.Geteuid(),
		PublicKey: publicKey, OS: "linux", Architecture: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(fetched.WorkDirectory)
	if fetched.Manifest.GenerationID != "generation" || fetched.Release.Manifest.Version != "1.2.3" {
		t.Fatalf("fetched control = %+v / %+v", fetched.Manifest, fetched.Release.Manifest)
	}
	inspection, err := state.InspectDatabase(ctx, fetched.DatabasePath, os.Geteuid(), true)
	if err != nil || inspection.SchemaVersion != state.SupportedSchemaVersion() {
		t.Fatalf("fetched SQLite = %+v, %v", inspection, err)
	}
}

func TestFetchControlRejectsArchitectureBeforeLeavingFiles(t *testing.T) {
	t.Parallel()
	master := cryptobox.MasterKey{10, 11, 12}
	remote := newMemoryControlRemote()
	// The payload only needs a valid encrypted manifest because architecture is
	// rejected before any database or release file is extracted.
	manifest, err := json.Marshal(ControlManifest{
		FormatVersion: ControlFormatVersion, InstallationID: "installation", GenerationID: "generation",
		CreatedAtMillis: time.Unix(10, 0).UnixMilli(), OS: "linux", Architecture: "amd64", PlatformVersion: "1.2.3", SchemaVersion: 1,
		Database:        ControlFile{Size: 1, SHA256: strings.Repeat("a", 64)},
		ReleaseManifest: ControlFile{Size: 1, SHA256: strings.Repeat("b", 64)},
		ReleaseBinary:   ControlFile{Size: 1, SHA256: strings.Repeat("c", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tarWriter := tar.NewWriter(&archive)
	if err := tarWriter.WriteHeader(&tar.Header{Name: controlManifestName, Mode: 0o600, Size: int64(len(manifest)), Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	built := controlPublicationBuild(t, master, "generation", archive.Bytes())
	defer os.RemoveAll(built.WorkDirectory)
	if err := PublishControl(context.Background(), remote, master, built); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = FetchControl(context.Background(), ControlFetchConfig{
		Remote: remote, Master: master, WorkRoot: filepath.Join(root, "restore"), ExpectedUID: os.Geteuid(),
		PublicKey: publicKey, OS: "linux", Architecture: "arm64",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot restore") {
		t.Fatalf("architecture mismatch = %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "restore"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed restore work root = %v, %v", entries, readErr)
	}
}

func controlPublicationBuild(t *testing.T, master cryptobox.MasterKey, generationID string, plaintext []byte) ControlBuild {
	t.Helper()
	cipher, err := backupcrypto.NewControlCipher(master, "installation")
	if err != nil {
		t.Fatal(err)
	}
	sealed, chunk, err := cipher.SealChunk(generationID, 0, plaintext, bytes.NewReader(bytes.Repeat([]byte{0x33}, 24)))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	path := work + "/chunk-00000000.pdx"
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := ControlEnvelope{
		FormatVersion: ControlFormatVersion, InstallationID: "installation", GenerationID: generationID,
		CreatedAtMillis: time.Unix(10, 0).UnixMilli(), Chunks: []backupcrypto.Chunk{chunk},
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	envelopeHash := sha256.Sum256(envelopeBytes)
	completion := ControlCompletion{
		FormatVersion: ControlFormatVersion, InstallationID: "installation", GenerationID: generationID,
		EnvelopeSHA256: hex.EncodeToString(envelopeHash[:]), CompletedAtMillis: time.Unix(11, 0).UnixMilli(),
	}
	completionBytes, err := json.Marshal(completion)
	if err != nil {
		t.Fatal(err)
	}
	return ControlBuild{
		Envelope: envelope, EnvelopeBytes: envelopeBytes, Completion: completion, CompletionBytes: completionBytes,
		Chunks: []backupcrypto.WorkChunk{{Chunk: chunk, Path: path}}, WorkDirectory: work,
	}
}
