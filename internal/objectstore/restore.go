package objectstore

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/iivankin/platformd/internal/state"
)

const (
	maximumBackupManifestSize       = 64 << 10
	maximumBackupObjectMetadataSize = 64 << 10
)

type RestoreInput struct {
	StoreID string
	Archive io.Reader
	Actor   Actor
}

func (application *Application) RestoreSnapshot(ctx context.Context, input RestoreInput) (string, error) {
	if ctx == nil || input.StoreID == "" || input.Archive == nil || input.Actor.ID == "" ||
		(input.Actor.Kind != "access" && input.Actor.Kind != "token" && input.Actor.Kind != "system") ||
		(input.Actor.Kind == "access" && input.Actor.Email == "") ||
		(input.Actor.Kind != "access" && input.Actor.Email != "") {
		return "", errors.New("object store restore input is invalid")
	}
	if _, err := application.repository.ObjectStore(ctx, input.StoreID); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp("", "platformd-objectstore-restore-*.tar")
	if err != nil {
		return "", fmt.Errorf("create object restore spool: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if _, err := io.Copy(temporary, restoreContextReader{ctx: ctx, source: input.Archive}); err != nil {
		return "", fmt.Errorf("spool object backup before restore: %w", err)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := readBackupArchive(temporary, input.StoreID, func(_ BackupObject, body io.Reader) error {
		_, err := io.Copy(io.Discard, body)
		return err
	}); err != nil {
		return "", err
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	releaseExclusion, err := application.beginBackupExclusion(input.StoreID)
	if err != nil {
		return "", err
	}
	defer releaseExclusion()
	releaseDataPlane, err := application.beginDataPlaneRestore(ctx, input.StoreID)
	if err != nil {
		return "", err
	}
	defer func() { _ = releaseDataPlane() }()
	releaseRequests, err := application.blockRequestsForRestore(ctx, input.StoreID)
	if err != nil {
		return "", err
	}
	defer releaseRequests()
	releaseWrites, err := application.blockMetadataForRestore(ctx, input.StoreID)
	if err != nil {
		return "", err
	}
	defer releaseWrites()
	if err := application.storage.EnsureBucket(ctx, input.StoreID); err != nil {
		return "", err
	}
	if err := application.storage.ClearBucket(ctx, input.StoreID); err != nil {
		return "", err
	}
	objectCount, err := readBackupArchive(temporary, input.StoreID, func(object BackupObject, body io.Reader) error {
		written, err := application.storage.Put(ctx, PutInput{
			StoreID: input.StoreID, ObjectKey: object.Key, ContentType: object.ContentType,
			Body: body, BodySize: object.Size, BodySizeKnown: true,
			PreserveETag: object.ETag, ModTimeMillis: object.UpdatedAtMillis,
		})
		if err != nil {
			return err
		}
		if written.Size != object.Size || written.ETag != object.ETag {
			return errors.New("restored object metadata differs from backup metadata")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(2)
	if err != nil {
		return "", err
	}
	if err := application.repository.RecordObjectStoreRestore(ctx, state.RecordObjectStoreRestore{
		ObjectStoreID: input.StoreID, ObjectCount: objectCount, AuditEventID: identifiers[0],
		ActorKind: input.Actor.Kind, ActorID: input.Actor.ID, ActorEmail: input.Actor.Email,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: timestamp.UnixMilli(),
	}); err != nil {
		return "", err
	}
	return identifiers[1], nil
}

func readBackupArchive(input io.Reader, storeID string, consume func(BackupObject, io.Reader) error) (int, error) {
	archive := tar.NewReader(input)
	header, err := archive.Next()
	if err != nil {
		return 0, fmt.Errorf("read object backup manifest: %w", err)
	}
	if header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Mode != 0o600 ||
		header.Size < 1 || header.Size > maximumBackupManifestSize {
		return 0, errors.New("object backup manifest entry is invalid")
	}
	manifest, err := io.ReadAll(io.LimitReader(archive, maximumBackupManifestSize+1))
	if err != nil || int64(len(manifest)) != header.Size {
		return 0, errors.New("object backup manifest is truncated")
	}
	if _, err := decodeBackupSnapshot(manifest, storeID); err != nil {
		return 0, err
	}

	previous := ""
	for index := 0; ; index++ {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return index, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read object backup metadata %d: %w", index, err)
		}
		if header.Name != backupMetadataName(index) || header.Typeflag != tar.TypeReg ||
			header.Mode != 0o600 || header.Size < 1 || header.Size > maximumBackupObjectMetadataSize {
			return 0, errors.New("object backup metadata entry is invalid")
		}
		metadata, err := io.ReadAll(io.LimitReader(archive, maximumBackupObjectMetadataSize+1))
		if err != nil || int64(len(metadata)) != header.Size {
			return 0, errors.New("object backup metadata is truncated")
		}
		object, err := decodeBackupObject(metadata, previous)
		if err != nil {
			return 0, err
		}
		header, err = archive.Next()
		if err != nil {
			return 0, fmt.Errorf("read object backup data %d: %w", index, err)
		}
		if header.Name != backupDataName(index) || header.Typeflag != tar.TypeReg ||
			header.Mode != 0o600 || header.Size != object.Size {
			return 0, errors.New("object backup data entry is invalid")
		}
		body := &io.LimitedReader{R: archive, N: object.Size}
		if err := consume(object, body); err != nil {
			return 0, err
		}
		if body.N != 0 {
			return 0, errors.New("object backup data is truncated")
		}
		previous = object.Key
	}
}

type restoreContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader restoreContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
