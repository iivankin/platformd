package objectstore

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const BackupFormatVersion = 1

type BackupSnapshot struct {
	FormatVersion int    `json:"formatVersion"`
	StoreID       string `json:"storeId"`
}

type BackupObject struct {
	Key             string `json:"key"`
	ContentType     string `json:"contentType,omitempty"`
	ETag            string `json:"etag"`
	Size            int64  `json:"size"`
	UpdatedAtMillis int64  `json:"updatedAt"`
}

type BackupExport struct {
	Reader  io.ReadCloser
	Release func()
}

func (application *Application) BackupSnapshot(ctx context.Context, storeID string) (BackupExport, error) {
	if _, err := application.repository.ObjectStore(ctx, storeID); err != nil {
		return BackupExport{}, err
	}
	releaseBackup, err := application.beginBackupExclusion(storeID)
	if err != nil {
		return BackupExport{}, err
	}
	releaseDataPlane, err := application.beginDataPlaneBackup(ctx, storeID)
	if err != nil {
		releaseBackup()
		return BackupExport{}, err
	}
	releaseWrites, err := application.blockMetadata(ctx, storeID)
	if err != nil {
		_ = releaseDataPlane()
		releaseBackup()
		return BackupExport{}, err
	}
	reader, writer := io.Pipe()
	go application.writeBackupArchive(ctx, writer, storeID)
	release := sync.OnceFunc(func() {
		releaseWrites()
		_ = releaseDataPlane()
		releaseBackup()
	})
	return BackupExport{Reader: reader, Release: release}, nil
}

func (application *Application) writeBackupArchive(ctx context.Context, pipe *io.PipeWriter, storeID string) {
	archive := tar.NewWriter(pipe)
	fail := func(err error) {
		_ = archive.Close()
		_ = pipe.CloseWithError(err)
	}
	manifest, err := json.Marshal(BackupSnapshot{
		FormatVersion: BackupFormatVersion, StoreID: storeID,
	})
	if err != nil {
		fail(err)
		return
	}
	if err := writeTarHeader(archive, "manifest.json", int64(len(manifest))); err != nil {
		fail(err)
		return
	}
	if _, err := archive.Write(manifest); err != nil {
		fail(err)
		return
	}
	const pageSize = 1000
	after := ""
	index := 0
	for {
		objects, more, err := application.List(ctx, storeID, "", after, pageSize)
		if err != nil {
			fail(err)
			return
		}
		for _, stored := range objects {
			object := BackupObject{
				Key: stored.ObjectKey, ContentType: stored.ContentType, ETag: stored.ETag,
				Size: stored.Size, UpdatedAtMillis: stored.UpdatedAtMillis,
			}
			metadata, err := json.Marshal(object)
			if err != nil {
				fail(err)
				return
			}
			if len(metadata) > maximumBackupObjectMetadataSize {
				fail(errors.New("object backup metadata exceeds the format limit"))
				return
			}
			if err := writeTarHeader(archive, backupMetadataName(index), int64(len(metadata))); err != nil {
				fail(err)
				return
			}
			if _, err := archive.Write(metadata); err != nil {
				fail(err)
				return
			}
			if err := writeTarHeader(archive, backupDataName(index), object.Size); err != nil {
				fail(err)
				return
			}
			if err := application.storage.ReadRange(ctx, storeID, object.Key, 0, object.Size, archive); err != nil {
				fail(err)
				return
			}
			index++
		}
		if !more || len(objects) == 0 {
			break
		}
		after = objects[len(objects)-1].ObjectKey
	}
	if err := archive.Close(); err != nil {
		_ = pipe.CloseWithError(err)
		return
	}
	_ = pipe.Close()
}

func backupMetadataName(index int) string { return fmt.Sprintf("objects/%012d.json", index) }
func backupDataName(index int) string     { return fmt.Sprintf("objects/%012d.data", index) }

func writeTarHeader(archive *tar.Writer, name string, size int64) error {
	return archive.WriteHeader(&tar.Header{
		Name: name, Mode: 0o600, Size: size, Typeflag: tar.TypeReg,
		Format: tar.FormatPAX,
	})
}

func decodeBackupSnapshot(value []byte, storeID string) (BackupSnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var snapshot BackupSnapshot
	if err := decoder.Decode(&snapshot); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return BackupSnapshot{}, errors.New("object backup manifest JSON is invalid")
	}
	if err := validateBackupSnapshot(snapshot, storeID); err != nil {
		return BackupSnapshot{}, err
	}
	return snapshot, nil
}

func validateBackupSnapshot(snapshot BackupSnapshot, storeID string) error {
	if snapshot.FormatVersion != BackupFormatVersion || snapshot.StoreID != storeID {
		return errors.New("object backup manifest identity is invalid")
	}
	return nil
}

func decodeBackupObject(value []byte, previous string) (BackupObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var object BackupObject
	if err := decoder.Decode(&object); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		validateObjectKey(object.Key) != nil || (previous != "" && object.Key <= previous) ||
		object.Size < 0 || object.Size > MaximumObjectSize || object.ETag == "" ||
		object.UpdatedAtMillis <= 0 {
		return BackupObject{}, errors.New("object backup metadata is invalid")
	}
	return object, nil
}
