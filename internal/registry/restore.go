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
	"strings"

	"github.com/iivankin/platformd/internal/backupcron"
	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/strictjson"
)

func restoreBackupArchive(
	ctx context.Context,
	repositoryID string,
	input io.Reader,
	payloads *PayloadStore,
) (BackupSnapshot, error) {
	if ctx == nil || !safeComponent(repositoryID) || input == nil || payloads == nil {
		return BackupSnapshot{}, errors.New("registry backup restore input is invalid")
	}
	reader := tar.NewReader(input)
	header, err := reader.Next()
	if err != nil {
		return BackupSnapshot{}, fmt.Errorf("read registry backup manifest entry: %w", err)
	}
	if header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size <= 0 {
		return BackupSnapshot{}, errors.New("registry backup manifest tar entry is invalid")
	}
	metadata, err := io.ReadAll(reader)
	if err != nil || int64(len(metadata)) != header.Size {
		return BackupSnapshot{}, errors.Join(err, errors.New("registry backup manifest entry ended early"))
	}
	snapshot, err := decodeBackupSnapshot(metadata, repositoryID)
	if err != nil {
		return BackupSnapshot{}, err
	}
	for _, blob := range snapshot.Blobs {
		if err := ctx.Err(); err != nil {
			return BackupSnapshot{}, err
		}
		header, err = reader.Next()
		if err != nil {
			return BackupSnapshot{}, fmt.Errorf("read registry backup blob entry: %w", err)
		}
		expectedName := "blobs/sha256/" + strings.TrimPrefix(blob.Digest, "sha256:")
		if header.Name != expectedName || header.Typeflag != tar.TypeReg || header.Size != blob.Size {
			return BackupSnapshot{}, errors.New("registry backup blob tar entry differs from manifest")
		}
		if err := payloads.InstallBackupBlob(ctx, repositoryID, blob.Digest, blob.Size, reader); err != nil {
			return BackupSnapshot{}, fmt.Errorf("install registry backup blob %s: %w", blob.Digest, err)
		}
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		return BackupSnapshot{}, errors.Join(err, errors.New("registry backup tar contains unexpected entries"))
	}
	return snapshot, nil
}

func decodeBackupSnapshot(value []byte, repositoryID string) (BackupSnapshot, error) {
	if len(value) == 0 {
		return BackupSnapshot{}, errors.New("registry backup snapshot is empty")
	}
	if err := strictjson.RejectDuplicateKeys(value); err != nil {
		return BackupSnapshot{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var snapshot BackupSnapshot
	if err := decoder.Decode(&snapshot); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return BackupSnapshot{}, errors.New("registry backup snapshot JSON is invalid")
	}
	if err := validateBackupSnapshot(snapshot, repositoryID); err != nil {
		return BackupSnapshot{}, err
	}
	return snapshot, nil
}

func validateBackupSnapshot(snapshot BackupSnapshot, repositoryID string) error {
	if snapshot.FormatVersion != BackupFormatVersion || snapshot.Repository.ID != repositoryID ||
		registryname.ValidateRepository(snapshot.Repository.Name) != nil ||
		snapshot.Repository.BackupRetentionCount < 1 || snapshot.Repository.BackupRetentionCount > 100 ||
		(snapshot.Repository.BackupEnabled && snapshot.Repository.BackupCron == "") {
		return errors.New("registry backup repository metadata is invalid")
	}
	if snapshot.Repository.BackupCron != "" {
		canonical, err := backupcron.Canonical(snapshot.Repository.BackupCron)
		if err != nil || canonical != snapshot.Repository.BackupCron {
			return errors.New("registry backup cron is invalid or non-canonical")
		}
	}
	if len(snapshot.Manifests) > MaximumManifestsPerRepository {
		return errors.New("registry backup manifest quota is exceeded")
	}

	blobs := make(map[string]BackupBlob, len(snapshot.Blobs))
	previous := ""
	for _, blob := range snapshot.Blobs {
		if registryname.ValidateDigest(blob.Digest) != nil || blob.Size < 0 ||
			(previous != "" && blob.Digest <= previous) {
			return errors.New("registry backup blobs are invalid or not canonically sorted")
		}
		previous = blob.Digest
		blobs[blob.Digest] = blob
	}

	manifests := make(map[string]BackupManifest, len(snapshot.Manifests))
	references := make(map[string]struct{})
	previous = ""
	for _, manifest := range snapshot.Manifests {
		if registryname.ValidateDigest(manifest.Digest) != nil || manifest.PushedAtMillis <= 0 ||
			(previous != "" && manifest.Digest <= previous) {
			return errors.New("registry backup manifests are invalid or not canonically sorted")
		}
		previous = manifest.Digest
		actualDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifest.Body))
		mediaType, referenced, index, err := validateManifest(manifest.MediaType, manifest.Body)
		if err != nil || mediaType != manifest.MediaType || actualDigest != manifest.Digest {
			return errors.Join(err, errors.New("registry backup manifest content is invalid"))
		}
		manifests[manifest.Digest] = manifest
		if index {
			for _, digest := range referenced {
				references["manifest\x00"+digest] = struct{}{}
			}
			continue
		}
		for _, digest := range referenced {
			references["blob\x00"+digest] = struct{}{}
		}
	}
	for reference := range references {
		kind, digest, _ := strings.Cut(reference, "\x00")
		if kind == "manifest" {
			if _, exists := manifests[digest]; !exists {
				return errors.New("registry backup index references a missing manifest")
			}
			continue
		}
		if _, exists := blobs[digest]; !exists {
			return errors.New("registry backup manifest references a missing blob")
		}
	}
	referencedBlobCount := 0
	for reference := range references {
		if strings.HasPrefix(reference, "blob\x00") {
			referencedBlobCount++
		}
	}
	if referencedBlobCount != len(blobs) {
		return errors.New("registry backup contains unreferenced blobs")
	}

	previous = ""
	for _, tag := range snapshot.Tags {
		if registryname.ValidateTag(tag.Name) != nil || registryname.ValidateDigest(tag.ManifestDigest) != nil ||
			tag.UpdatedAtMillis <= 0 || (previous != "" && tag.Name <= previous) {
			return errors.New("registry backup tags are invalid or not canonically sorted")
		}
		previous = tag.Name
		if _, exists := manifests[tag.ManifestDigest]; !exists {
			return errors.New("registry backup tag references a missing manifest")
		}
	}
	return nil
}
