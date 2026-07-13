package registry

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

const BackupFormatVersion = 1

type BackupSnapshot struct {
	FormatVersion int              `json:"formatVersion"`
	Repository    BackupRepository `json:"repository"`
	Manifests     []BackupManifest `json:"manifests"`
	Tags          []BackupTag      `json:"tags"`
	Blobs         []BackupBlob     `json:"blobs"`
}

type BackupRepository struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	PublicPull           bool   `json:"publicPull"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
}

type BackupBlob struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Path   string `json:"-"`
}

type BackupManifest struct {
	Digest         string `json:"digest"`
	MediaType      string `json:"mediaType"`
	Body           []byte `json:"body"`
	PushedAtMillis int64  `json:"pushedAt"`
}

type BackupTag struct {
	Name            string `json:"name"`
	ManifestDigest  string `json:"manifestDigest"`
	UpdatedAtMillis int64  `json:"updatedAt"`
}

type BackupExport struct {
	Reader  io.ReadCloser
	Release func()
}

func (application *Application) BackupSnapshot(ctx context.Context, repositoryID string) (BackupExport, error) {
	repository, err := application.store.RegistryRepository(ctx, repositoryID)
	if err != nil {
		return BackupExport{}, err
	}
	releaseMaintenance, err := application.beginRepositoryMaintenance(repositoryID, "backup")
	if err != nil {
		return BackupExport{}, err
	}
	lock := application.acquireRepositoryLock(repositoryID)
	snapshot, err := application.enumerateBackupSnapshot(ctx, repository)
	application.releaseRepositoryLock(repositoryID, lock)
	if err != nil {
		releaseMaintenance()
		return BackupExport{}, err
	}
	reader, writer := io.Pipe()
	go streamBackupTar(ctx, snapshot, writer)
	return BackupExport{Reader: reader, Release: releaseMaintenance}, nil
}

func (application *Application) enumerateBackupSnapshot(ctx context.Context, repository state.RegistryRepository) (BackupSnapshot, error) {
	const pageSize = 1000
	snapshot := BackupSnapshot{
		FormatVersion: BackupFormatVersion,
		Repository: BackupRepository{
			ID: repository.ID, Name: repository.Name, PublicPull: repository.PublicPull,
			BackupEnabled: repository.BackupEnabled, BackupCron: repository.BackupCron,
			BackupRetentionCount: repository.BackupRetentionCount,
		},
	}
	referenced := make(map[string]struct{})
	afterManifest := ""
	for {
		manifests, more, err := application.store.RegistryManifests(ctx, repository.ID, afterManifest, pageSize)
		if err != nil {
			return BackupSnapshot{}, err
		}
		for _, manifest := range manifests {
			snapshot.Manifests = append(snapshot.Manifests, BackupManifest{
				Digest: manifest.Digest, MediaType: manifest.MediaType,
				Body: manifest.Body, PushedAtMillis: manifest.PushedAtMillis,
			})
			if manifest.MediaType == OCIImageIndexMediaType || manifest.MediaType == DockerManifestListMediaType {
				continue
			}
			var document manifestDocument
			if err := json.Unmarshal(manifest.Body, &document); err != nil {
				return BackupSnapshot{}, fmt.Errorf("decode registry manifest for backup: %w", err)
			}
			referenced[document.Config.Digest] = struct{}{}
			for _, layer := range document.Layers {
				referenced[layer.Digest] = struct{}{}
			}
		}
		if !more || len(manifests) == 0 {
			break
		}
		afterManifest = manifests[len(manifests)-1].Digest
	}
	afterTag := ""
	for {
		tags, more, err := application.store.RegistryTags(ctx, repository.ID, afterTag, pageSize)
		if err != nil {
			return BackupSnapshot{}, err
		}
		for _, tag := range tags {
			snapshot.Tags = append(snapshot.Tags, BackupTag{
				Name: tag.Name, ManifestDigest: tag.ManifestDigest, UpdatedAtMillis: tag.UpdatedAtMillis,
			})
		}
		if !more || len(tags) == 0 {
			break
		}
		afterTag = tags[len(tags)-1].Name
	}
	digests := make([]string, 0, len(referenced))
	for digest := range referenced {
		digests = append(digests, digest)
	}
	slices.Sort(digests)
	for _, digest := range digests {
		path, size, err := application.payloads.BlobPath(repository.ID, digest)
		if err != nil {
			return BackupSnapshot{}, err
		}
		snapshot.Blobs = append(snapshot.Blobs, BackupBlob{Digest: digest, Size: size, Path: path})
	}
	return snapshot, nil
}

func streamBackupTar(ctx context.Context, snapshot BackupSnapshot, output *io.PipeWriter) {
	writer := tar.NewWriter(output)
	metadata, err := json.Marshal(snapshot)
	if err == nil {
		err = writeBackupTarEntry(ctx, writer, "manifest.json", int64(len(metadata)), bytes.NewReader(metadata))
	}
	for _, blob := range snapshot.Blobs {
		if err != nil {
			break
		}
		file, openErr := os.Open(blob.Path)
		if openErr != nil {
			err = openErr
			break
		}
		name := "blobs/sha256/" + strings.TrimPrefix(blob.Digest, "sha256:")
		writeErr := writeBackupTarEntry(ctx, writer, name, blob.Size, file)
		closeErr := file.Close()
		err = errors.Join(writeErr, closeErr)
	}
	closeErr := writer.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = output.CloseWithError(err)
		return
	}
	_ = output.Close()
}

func writeBackupTarEntry(ctx context.Context, writer *tar.Writer, name string, size int64, input io.Reader) error {
	if err := writer.WriteHeader(&tar.Header{
		Name: name, Mode: 0o600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatPAX,
	}); err != nil {
		return err
	}
	written, err := io.Copy(writer, &backupContextReader{ctx: ctx, source: input})
	if err != nil || written != size {
		return errors.Join(err, errors.New("registry backup tar entry size differs"))
	}
	return nil
}

type backupContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *backupContextReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(output)
}
