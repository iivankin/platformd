package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/iivankin/platformd/internal/state"
)

const BackupFormatVersion = 1

type BackupSnapshot struct {
	FormatVersion int                `json:"formatVersion"`
	StoreID       string             `json:"storeId"`
	Objects       []BackupObject     `json:"objects"`
	Payloads      []BackupPayload    `json:"payloads"`
	Attachments   []BackupAttachment `json:"attachments"`
}

type BackupObject struct {
	Key             string `json:"key"`
	PayloadID       string `json:"payloadId"`
	ContentType     string `json:"contentType,omitempty"`
	ETag            string `json:"etag"`
	Size            int64  `json:"size"`
	CreatedAtMillis int64  `json:"createdAt"`
	UpdatedAtMillis int64  `json:"updatedAt"`
}

type BackupPayload struct {
	ID              string `json:"id"`
	PlaintextSize   int64  `json:"plaintextSize"`
	ChunkCount      int    `json:"chunkCount"`
	PlaintextSHA256 string `json:"plaintextSha256"`
	CreatedAtMillis int64  `json:"createdAt"`
}

type BackupAttachment struct {
	Index      int    `json:"index"`
	PayloadID  string `json:"payloadId"`
	ChunkIndex int    `json:"chunkIndex"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

type BackupExport struct {
	Metadata        []byte
	AttachmentPaths []string
	Release         func()
}

func (application *Application) BackupSnapshot(ctx context.Context, storeID string) (BackupExport, error) {
	if _, err := application.repository.ObjectStore(ctx, storeID); err != nil {
		return BackupExport{}, err
	}
	releaseBackup, err := application.beginBackupExclusion(storeID)
	if err != nil {
		return BackupExport{}, err
	}
	releaseMetadata, err := application.blockMetadata(ctx, storeID)
	if err != nil {
		releaseBackup()
		return BackupExport{}, err
	}
	snapshot, paths, err := application.enumerateBackupSnapshot(ctx, storeID)
	releaseMetadata()
	if err != nil {
		releaseBackup()
		return BackupExport{}, err
	}
	metadata, err := json.Marshal(snapshot)
	if err != nil {
		releaseBackup()
		return BackupExport{}, err
	}
	return BackupExport{Metadata: metadata, AttachmentPaths: paths, Release: releaseBackup}, nil
}

func (application *Application) enumerateBackupSnapshot(ctx context.Context, storeID string) (BackupSnapshot, []string, error) {
	const pageSize = 1000
	snapshot := BackupSnapshot{FormatVersion: BackupFormatVersion, StoreID: storeID}
	paths := make([]string, 0)
	seenPayloads := make(map[string]struct{})
	after := ""
	for {
		objects, more, err := application.repository.ListObjects(ctx, storeID, "", after, pageSize)
		if err != nil {
			return BackupSnapshot{}, nil, err
		}
		for _, object := range objects {
			snapshot.Objects = append(snapshot.Objects, BackupObject{
				Key: object.ObjectKey, PayloadID: object.PayloadID, ContentType: object.ContentType,
				ETag: object.ETag, Size: object.Size, CreatedAtMillis: object.CreatedAtMillis,
				UpdatedAtMillis: object.UpdatedAtMillis,
			})
			if _, exists := seenPayloads[object.PayloadID]; exists {
				continue
			}
			payload, err := application.repository.ObjectPayload(ctx, storeID, object.PayloadID)
			if err != nil {
				return BackupSnapshot{}, nil, err
			}
			if err := validateBackupPayload(payload); err != nil {
				return BackupSnapshot{}, nil, err
			}
			seenPayloads[payload.ID] = struct{}{}
			snapshot.Payloads = append(snapshot.Payloads, BackupPayload{
				ID: payload.ID, PlaintextSize: payload.PlaintextSize, ChunkCount: payload.ChunkCount,
				PlaintextSHA256: payload.PlaintextSHA256, CreatedAtMillis: payload.CreatedAtMillis,
			})
			for chunkIndex := 0; chunkIndex < payload.ChunkCount; chunkIndex++ {
				chunk, err := application.payloads.BackupChunk(ctx, storeID, payload.ID, chunkIndex)
				if err != nil {
					return BackupSnapshot{}, nil, err
				}
				index := len(paths)
				paths = append(paths, chunk.Path)
				snapshot.Attachments = append(snapshot.Attachments, BackupAttachment{
					Index: index, PayloadID: payload.ID, ChunkIndex: chunkIndex,
					Size: chunk.Size, SHA256: chunk.SHA256,
				})
			}
		}
		if !more || len(objects) == 0 {
			break
		}
		after = objects[len(objects)-1].ObjectKey
	}
	return snapshot, paths, nil
}

func validateBackupPayload(payload state.ObjectPayload) error {
	if payload.ID == "" || payload.PlaintextSize < 0 || payload.ChunkCount < 0 || payload.PlaintextSHA256 == "" || payload.CreatedAtMillis <= 0 {
		return errors.New("object backup payload metadata is invalid")
	}
	expectedChunks := int64(0)
	if payload.PlaintextSize > 0 {
		if payload.PlaintextSize > math.MaxInt64-(ChunkSize-1) {
			return errors.New("object backup payload size overflows")
		}
		expectedChunks = (payload.PlaintextSize + ChunkSize - 1) / ChunkSize
	}
	if expectedChunks != int64(payload.ChunkCount) {
		return errors.New("object backup payload chunk count differs from size")
	}
	return nil
}
