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
	"sort"
	"strings"

	"github.com/iivankin/platformd/internal/backupcrypto"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

const maximumControlManifestSize = 64 << 20

type ControlFetchConfig struct {
	Remote       ControlRemote
	Master       cryptobox.MasterKey
	WorkRoot     string
	ExpectedUID  int
	PublicKey    ed25519.PublicKey
	OS           string
	Architecture string
}

type FetchedControl struct {
	Completion          ControlCompletion
	Envelope            ControlEnvelope
	Manifest            ControlManifest
	DatabasePath        string
	ReleaseManifestPath string
	ReleaseBinaryPath   string
	Release             bootstrap.VerifiedRelease
	WorkDirectory       string
}

func FetchControl(ctx context.Context, config ControlFetchConfig) (FetchedControl, error) {
	if config.Remote == nil || !safeBackupRoot(config.WorkRoot) || config.ExpectedUID < 0 ||
		len(config.PublicKey) != ed25519.PublicKeySize || config.OS == "" || config.Architecture == "" {
		return FetchedControl{}, errors.New("control fetch configuration is incomplete")
	}
	completionBytes, err := readRemoteObject(ctx, config.Remote, config.Remote.Key(ControlCompletionKey()), 64<<10)
	if err != nil {
		return FetchedControl{}, fmt.Errorf("read control completion: %w", err)
	}
	completion, err := DecodeControlCompletion(completionBytes)
	if err != nil {
		return FetchedControl{}, err
	}
	envelopeBytes, err := readRemoteObject(
		ctx, config.Remote, config.Remote.Key(ControlEnvelopeKey(completion.GenerationID)), maximumEnvelopeSize,
	)
	if err != nil {
		return FetchedControl{}, fmt.Errorf("read control envelope: %w", err)
	}
	envelopeHash := sha256.Sum256(envelopeBytes)
	if hex.EncodeToString(envelopeHash[:]) != completion.EnvelopeSHA256 {
		return FetchedControl{}, errors.New("control envelope checksum differs from completion marker")
	}
	envelope, err := DecodeControlEnvelope(envelopeBytes)
	if err != nil {
		return FetchedControl{}, err
	}
	if envelope.InstallationID != completion.InstallationID || envelope.GenerationID != completion.GenerationID {
		return FetchedControl{}, errors.New("control envelope identity differs from completion marker")
	}
	cipher, err := backupcrypto.NewControlCipher(config.Master, envelope.InstallationID)
	if err != nil {
		return FetchedControl{}, err
	}
	if err := os.MkdirAll(config.WorkRoot, 0o700); err != nil {
		return FetchedControl{}, err
	}
	workDirectory := filepath.Join(config.WorkRoot, "restore-"+envelope.GenerationID)
	if err := os.Mkdir(workDirectory, 0o700); err != nil {
		return FetchedControl{}, err
	}
	cleanup := func(err error) (FetchedControl, error) {
		_ = os.RemoveAll(workDirectory)
		return FetchedControl{}, err
	}

	stream := &controlChunkReader{ctx: ctx, remote: config.Remote, cipher: cipher, envelope: envelope}
	archive := tar.NewReader(stream)
	header, err := nextControlEntry(archive, controlManifestName, 0o600)
	if err != nil || header.Size < 1 || header.Size > maximumControlManifestSize {
		return cleanup(errors.Join(err, errors.New("control manifest archive entry is invalid")))
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(archive, maximumControlManifestSize+1))
	if err != nil || int64(len(manifestBytes)) != header.Size {
		return cleanup(errors.Join(err, errors.New("control manifest archive size differs")))
	}
	manifest, err := decodeControlManifest(manifestBytes)
	if err != nil {
		return cleanup(err)
	}
	if manifest.InstallationID != envelope.InstallationID || manifest.GenerationID != envelope.GenerationID ||
		manifest.CreatedAtMillis != envelope.CreatedAtMillis || completion.CompletedAtMillis < manifest.CreatedAtMillis {
		return cleanup(errors.New("encrypted control manifest identity differs from envelope"))
	}
	if manifest.OS != config.OS || manifest.Architecture != config.Architecture {
		return cleanup(fmt.Errorf("control architecture %s/%s cannot restore on %s/%s", manifest.OS, manifest.Architecture, config.OS, config.Architecture))
	}

	databasePath := filepath.Join(workDirectory, controlDatabaseName)
	if err := extractControlEntry(archive, controlDatabaseName, 0o600, manifest.Database, databasePath); err != nil {
		return cleanup(err)
	}
	releaseManifestPath := filepath.Join(workDirectory, releaseManifestName)
	if err := extractControlEntry(archive, releaseManifestName, 0o644, manifest.ReleaseManifest, releaseManifestPath); err != nil {
		return cleanup(err)
	}
	releaseBinaryPath := filepath.Join(workDirectory, releaseBinaryName)
	if err := extractControlEntry(archive, releaseBinaryName, 0o755, manifest.ReleaseBinary, releaseBinaryPath); err != nil {
		return cleanup(err)
	}
	if header, nextErr := archive.Next(); nextErr != io.EOF || header != nil {
		return cleanup(errors.Join(nextErr, errors.New("control archive contains trailing entries")))
	}
	if trailing, drainErr := io.Copy(io.Discard, stream); drainErr != nil || trailing != 0 {
		return cleanup(errors.Join(drainErr, errors.New("control archive contains trailing plaintext")))
	}

	releaseManifestBytes, err := os.ReadFile(releaseManifestPath)
	if err != nil {
		return cleanup(err)
	}
	release, err := bootstrap.VerifyRelease(releaseBinaryPath, releaseManifestBytes, config.PublicKey)
	if err != nil {
		return cleanup(fmt.Errorf("verify restored release: %w", err))
	}
	if release.Manifest.Version != manifest.PlatformVersion || release.Manifest.OS != manifest.OS ||
		release.Manifest.Architecture != manifest.Architecture {
		return cleanup(errors.New("restored signed release differs from encrypted control manifest"))
	}
	inspection, err := state.InspectDatabase(ctx, databasePath, config.ExpectedUID, true)
	if err != nil {
		return cleanup(fmt.Errorf("inspect restored SQLite: %w", err))
	}
	if inspection.SchemaVersion != manifest.SchemaVersion {
		return cleanup(errors.New("restored SQLite schema differs from encrypted control manifest"))
	}
	if err := syncControlDirectory(workDirectory); err != nil {
		return cleanup(err)
	}
	return FetchedControl{
		Completion: completion, Envelope: envelope, Manifest: manifest,
		DatabasePath: databasePath, ReleaseManifestPath: releaseManifestPath,
		ReleaseBinaryPath: releaseBinaryPath, Release: release, WorkDirectory: workDirectory,
	}, nil
}

func decodeControlManifest(value []byte) (ControlManifest, error) {
	if len(value) == 0 || len(value) > maximumControlManifestSize {
		return ControlManifest{}, errors.New("control manifest size is outside bounds")
	}
	if err := rejectDuplicateJSONKeys(value); err != nil {
		return ControlManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var manifest ControlManifest
	if err := decoder.Decode(&manifest); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ControlManifest{}, errors.New("control manifest JSON is invalid")
	}
	if manifest.FormatVersion != ControlFormatVersion || !validControlIdentifier(manifest.InstallationID) ||
		!validControlIdentifier(manifest.GenerationID) || manifest.CreatedAtMillis <= 0 || manifest.OS != "linux" ||
		manifest.Architecture != "amd64" || manifest.PlatformVersion == "" || manifest.SchemaVersion < 1 ||
		!validControlFile(manifest.Database) || !validControlFile(manifest.ReleaseManifest) ||
		!validControlFile(manifest.ReleaseBinary) || manifest.ReleaseManifest.Size > 64<<10 ||
		!validControlResourceIDs(manifest.Resources) {
		return ControlManifest{}, errors.New("control manifest fields are invalid")
	}
	return manifest, nil
}

func validControlFile(file ControlFile) bool {
	if file.Size < 1 || len(file.SHA256) != sha256.Size*2 || strings.ToLower(file.SHA256) != file.SHA256 {
		return false
	}
	_, err := hex.DecodeString(file.SHA256)
	return err == nil
}

func validControlResourceIDs(resources state.ControlResourceIDs) bool {
	groups := [][]string{resources.RegistryRepositories, resources.ObjectStores, resources.Postgres, resources.Redis, resources.Volumes}
	for _, group := range groups {
		if !sort.StringsAreSorted(group) {
			return false
		}
		for index, identifier := range group {
			if !validControlIdentifier(identifier) || index > 0 && group[index-1] == identifier {
				return false
			}
		}
	}
	return true
}

func nextControlEntry(archive *tar.Reader, name string, mode int64) (*tar.Header, error) {
	header, err := archive.Next()
	if err != nil {
		return nil, err
	}
	if header.Name != name || header.Mode != mode || header.Size < 0 ||
		(header.Typeflag != tar.TypeReg && header.Typeflag != 0) || header.Linkname != "" ||
		len(header.PAXRecords) != 0 || len(header.Xattrs) != 0 {
		return nil, fmt.Errorf("control archive entry %q metadata is invalid", name)
	}
	return header, nil
}

func extractControlEntry(archive *tar.Reader, name string, mode int64, expected ControlFile, destination string) error {
	header, err := nextControlEntry(archive, name, mode)
	if err != nil {
		return err
	}
	if header.Size != expected.Size {
		return fmt.Errorf("control archive entry %q size differs", name)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(mode))
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), archive)
	if copyErr == nil && written != expected.Size {
		copyErr = io.ErrUnexpectedEOF
	}
	if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		copyErr = fmt.Errorf("control archive entry %q checksum differs", name)
	}
	if copyErr == nil {
		copyErr = file.Chmod(os.FileMode(mode))
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return errors.Join(copyErr, closeErr)
	}
	return nil
}

type controlChunkReader struct {
	ctx      context.Context
	remote   ControlRemote
	cipher   *backupcrypto.ResourceCipher
	envelope ControlEnvelope
	index    int
	current  []byte
	offset   int
}

func (reader *controlChunkReader) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	for reader.offset == len(reader.current) {
		clear(reader.current)
		reader.current = nil
		reader.offset = 0
		if reader.index == len(reader.envelope.Chunks) {
			return 0, io.EOF
		}
		chunk := reader.envelope.Chunks[reader.index]
		sealed, err := readRemoteObject(
			reader.ctx, reader.remote, reader.remote.Key(ControlChunkKey(reader.envelope.GenerationID, chunk.Index)), int64(chunk.CiphertextSize),
		)
		if err != nil {
			return 0, err
		}
		plaintext, err := reader.cipher.OpenChunk(reader.envelope.GenerationID, chunk, sealed)
		clear(sealed)
		if err != nil {
			return 0, fmt.Errorf("decrypt control chunk %d: %w", chunk.Index, err)
		}
		reader.current = plaintext
		reader.index++
	}
	count := copy(output, reader.current[reader.offset:])
	reader.offset += count
	return count, nil
}

func syncControlDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
