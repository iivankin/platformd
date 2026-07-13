package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/state"
)

const (
	ControlFormatVersion = 1
	controlManifestName  = "control-manifest.json"
	controlDatabaseName  = "platformd.db"
	releaseManifestName  = "release-manifest.json"
	releaseBinaryName    = "platformd"
	maximumEnvelopeSize  = 1 << 20
)

type ControlFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ControlManifest struct {
	FormatVersion   int                      `json:"formatVersion"`
	InstallationID  string                   `json:"installationId"`
	GenerationID    string                   `json:"generationId"`
	CreatedAtMillis int64                    `json:"createdAt"`
	OS              string                   `json:"os"`
	Architecture    string                   `json:"architecture"`
	PlatformVersion string                   `json:"platformVersion"`
	SchemaVersion   int                      `json:"schemaVersion"`
	Database        ControlFile              `json:"database"`
	ReleaseManifest ControlFile              `json:"releaseManifest"`
	ReleaseBinary   ControlFile              `json:"releaseBinary"`
	Resources       state.ControlResourceIDs `json:"resources"`
}

type ControlEnvelope struct {
	FormatVersion   int                  `json:"formatVersion"`
	InstallationID  string               `json:"installationId"`
	GenerationID    string               `json:"generationId"`
	CreatedAtMillis int64                `json:"createdAt"`
	Chunks          []backupcrypto.Chunk `json:"chunks"`
}

type ControlCompletion struct {
	FormatVersion     int    `json:"formatVersion"`
	InstallationID    string `json:"installationId"`
	GenerationID      string `json:"generationId"`
	EnvelopeSHA256    string `json:"envelopeSha256"`
	CompletedAtMillis int64  `json:"completedAt"`
}

type ControlBuildConfig struct {
	Store          *state.Store
	Master         cryptobox.MasterKey
	InstallationID string
	GenerationID   string
	ReleaseSlot    string
	WorkRoot       string
	ExpectedUID    int
	PublicKey      ed25519.PublicKey
	CreatedAt      time.Time
	Random         io.Reader
}

type ControlBuild struct {
	Manifest        ControlManifest
	Envelope        ControlEnvelope
	EnvelopeBytes   []byte
	Completion      ControlCompletion
	CompletionBytes []byte
	Chunks          []backupcrypto.WorkChunk
	WorkDirectory   string
}

func BuildControl(ctx context.Context, config ControlBuildConfig) (ControlBuild, error) {
	if config.Store == nil || !validControlIdentifier(config.InstallationID) || !validControlIdentifier(config.GenerationID) ||
		!safeBackupRoot(config.ReleaseSlot) || !safeBackupRoot(config.WorkRoot) || config.ExpectedUID < 0 ||
		len(config.PublicKey) != ed25519.PublicKeySize || config.CreatedAt.IsZero() {
		return ControlBuild{}, errors.New("control backup build configuration is incomplete")
	}
	if err := bootstrap.VerifyReleaseSlot(config.ReleaseSlot, nil, config.PublicKey, config.ExpectedUID); err != nil {
		return ControlBuild{}, fmt.Errorf("verify control release slot: %w", err)
	}
	workDirectory := filepath.Join(config.WorkRoot, "control-"+config.GenerationID)
	if err := os.MkdirAll(config.WorkRoot, 0o700); err != nil {
		return ControlBuild{}, err
	}
	if err := os.Mkdir(workDirectory, 0o700); err != nil {
		return ControlBuild{}, err
	}
	cleanup := func(err error) (ControlBuild, error) {
		_ = os.RemoveAll(workDirectory)
		return ControlBuild{}, err
	}
	snapshotPath := filepath.Join(workDirectory, "snapshot.db")
	if err := config.Store.OnlineBackup(ctx, snapshotPath); err != nil {
		return cleanup(err)
	}
	defer os.Remove(snapshotPath)

	snapshot, err := state.Open(ctx, snapshotPath, config.ExpectedUID)
	if err != nil {
		return cleanup(err)
	}
	resources, resourcesErr := snapshot.ControlResources(ctx)
	schemaVersion, schemaErr := state.ReadSchemaVersion(ctx, snapshotPath, config.ExpectedUID)
	closeErr := snapshot.Close()
	if resourcesErr != nil || schemaErr != nil || closeErr != nil {
		return cleanup(errors.Join(resourcesErr, schemaErr, closeErr))
	}
	releaseManifestPath := filepath.Join(config.ReleaseSlot, releaseManifestName)
	releaseBinaryPath := filepath.Join(config.ReleaseSlot, releaseBinaryName)
	releaseManifestBytes, err := os.ReadFile(releaseManifestPath)
	if err != nil || len(releaseManifestBytes) > 64<<10 {
		return cleanup(errors.New("read release manifest for control backup failed"))
	}
	release, err := releasemanifest.ParseAndVerify(releaseManifestBytes, config.PublicKey)
	if err != nil {
		return cleanup(err)
	}
	databaseFile, err := inspectControlFile(snapshotPath)
	if err != nil {
		return cleanup(err)
	}
	releaseManifestFile, err := inspectControlFile(releaseManifestPath)
	if err != nil {
		return cleanup(err)
	}
	releaseBinaryFile, err := inspectControlFile(releaseBinaryPath)
	if err != nil {
		return cleanup(err)
	}
	manifest := ControlManifest{
		FormatVersion: ControlFormatVersion, InstallationID: config.InstallationID,
		GenerationID: config.GenerationID, CreatedAtMillis: config.CreatedAt.UnixMilli(),
		OS: release.OS, Architecture: release.Architecture, PlatformVersion: release.Version,
		SchemaVersion: schemaVersion, Database: databaseFile,
		ReleaseManifest: releaseManifestFile, ReleaseBinary: releaseBinaryFile, Resources: resources,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return cleanup(err)
	}
	cipher, err := backupcrypto.NewControlCipher(config.Master, config.InstallationID)
	if err != nil {
		return cleanup(err)
	}
	writer, err := backupcrypto.NewWorkWriter(cipher, config.GenerationID, workDirectory, config.Random)
	if err != nil {
		return cleanup(err)
	}
	archive := tar.NewWriter(writer)
	files := []struct {
		name    string
		path    string
		content []byte
		mode    int64
	}{
		{name: controlManifestName, content: manifestBytes, mode: 0o600},
		{name: controlDatabaseName, path: snapshotPath, mode: 0o600},
		{name: releaseManifestName, path: releaseManifestPath, mode: 0o644},
		{name: releaseBinaryName, path: releaseBinaryPath, mode: 0o755},
	}
	for _, file := range files {
		if err := writeControlTarFile(archive, file.name, file.path, file.content, file.mode); err != nil {
			_ = archive.Close()
			_ = writer.Close()
			return cleanup(err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = writer.Close()
		return cleanup(err)
	}
	if err := writer.Close(); err != nil {
		return cleanup(err)
	}
	chunks, err := writer.Chunks()
	if err != nil {
		return cleanup(err)
	}
	descriptors := make([]backupcrypto.Chunk, len(chunks))
	for index, chunk := range chunks {
		descriptors[index] = chunk.Chunk
	}
	envelope := ControlEnvelope{
		FormatVersion: ControlFormatVersion, InstallationID: config.InstallationID,
		GenerationID: config.GenerationID, CreatedAtMillis: config.CreatedAt.UnixMilli(), Chunks: descriptors,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil || len(envelopeBytes) > maximumEnvelopeSize {
		return cleanup(errors.New("control envelope exceeds size limit"))
	}
	envelopeHash := sha256.Sum256(envelopeBytes)
	completion := ControlCompletion{
		FormatVersion: ControlFormatVersion, InstallationID: config.InstallationID,
		GenerationID: config.GenerationID, EnvelopeSHA256: hex.EncodeToString(envelopeHash[:]),
		CompletedAtMillis: config.CreatedAt.UnixMilli(),
	}
	completionBytes, err := json.Marshal(completion)
	if err != nil {
		return cleanup(err)
	}
	return ControlBuild{
		Manifest: manifest, Envelope: envelope, EnvelopeBytes: envelopeBytes,
		Completion: completion, CompletionBytes: completionBytes,
		Chunks: chunks, WorkDirectory: workDirectory,
	}, nil
}

func inspectControlFile(path string) (ControlFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return ControlFile{}, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		_ = file.Close()
		return ControlFile{}, errors.New("control backup source is not a non-empty regular file")
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return ControlFile{}, errors.Join(copyErr, closeErr)
	}
	return ControlFile{Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func writeControlTarFile(archive *tar.Writer, name, path string, content []byte, mode int64) error {
	size := int64(len(content))
	if path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		size = info.Size()
	}
	header := &tar.Header{Name: name, Mode: mode, Size: size, Format: tar.FormatUSTAR}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	if path == "" {
		_, err := archive.Write(content)
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(archive, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != size {
		return errors.Join(copyErr, closeErr, fmt.Errorf("control archive file %s changed while reading", name))
	}
	return nil
}

func safeBackupRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func DecodeControlEnvelope(value []byte) (ControlEnvelope, error) {
	if len(value) == 0 || len(value) > maximumEnvelopeSize {
		return ControlEnvelope{}, errors.New("control envelope size is outside bounds")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ControlEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var envelope ControlEnvelope
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ControlEnvelope{}, errors.New("control envelope JSON is invalid")
	}
	if envelope.FormatVersion != ControlFormatVersion || !validControlIdentifier(envelope.InstallationID) ||
		!validControlIdentifier(envelope.GenerationID) ||
		envelope.CreatedAtMillis <= 0 || len(envelope.Chunks) == 0 || len(envelope.Chunks) > 1_000_000 {
		return ControlEnvelope{}, errors.New("control envelope fields are invalid")
	}
	for index, chunk := range envelope.Chunks {
		if chunk.Index != index || chunk.PlaintextSize < 0 || chunk.PlaintextSize > backupcrypto.DefaultChunkSize ||
			chunk.CiphertextSize <= 0 || len(chunk.CiphertextSHA) != sha256.Size*2 {
			return ControlEnvelope{}, errors.New("control envelope chunk descriptors are invalid")
		}
	}
	return envelope, nil
}

func DecodeControlCompletion(value []byte) (ControlCompletion, error) {
	if len(value) == 0 || len(value) > 64<<10 {
		return ControlCompletion{}, errors.New("control completion size is outside bounds")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ControlCompletion{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var completion ControlCompletion
	if err := decoder.Decode(&completion); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ControlCompletion{}, errors.New("control completion JSON is invalid")
	}
	if completion.FormatVersion != ControlFormatVersion || !validControlIdentifier(completion.InstallationID) ||
		!validControlIdentifier(completion.GenerationID) || completion.CompletedAtMillis <= 0 ||
		len(completion.EnvelopeSHA256) != sha256.Size*2 || strings.ToLower(completion.EnvelopeSHA256) != completion.EnvelopeSHA256 {
		return ControlCompletion{}, errors.New("control completion fields are invalid")
	}
	if _, err := hex.DecodeString(completion.EnvelopeSHA256); err != nil {
		return ControlCompletion{}, errors.New("control completion envelope checksum is invalid")
	}
	return completion, nil
}

func rejectDuplicateJSONKeys(value []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := walkJSONValue(decoder); err != nil {
		return errors.New("control metadata JSON contains duplicate keys or is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("control metadata JSON contains trailing data")
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}

func validControlIdentifier(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func ControlGenerationPrefix(generationID string) string {
	return "control/generations/" + generationID
}

func ControlChunkKey(generationID string, index int) string {
	return fmt.Sprintf("%s/chunk-%08d.pdx", ControlGenerationPrefix(generationID), index)
}

func ControlEnvelopeKey(generationID string) string {
	return ControlGenerationPrefix(generationID) + "/envelope.json"
}

func ControlCompletionKey() string { return "control/complete.json" }
