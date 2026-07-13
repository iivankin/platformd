package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/strictjson"
	"golang.org/x/crypto/chacha20poly1305"
)

type RestoreInput struct {
	StoreID             string
	Metadata            []byte
	ValidateAttachments func([]BackupAttachment) error
	OpenAttachment      BackupAttachmentOpener
	Actor               Actor
}

func (application *Application) RestoreSnapshot(ctx context.Context, input RestoreInput) (string, error) {
	if ctx == nil || input.StoreID == "" || len(input.Metadata) == 0 || input.ValidateAttachments == nil || input.Actor.ID == "" ||
		(input.Actor.Kind != "access" && input.Actor.Kind != "token" && input.Actor.Kind != "system") ||
		(input.Actor.Kind == "access" && input.Actor.Email == "") ||
		(input.Actor.Kind != "access" && input.Actor.Email != "") {
		return "", errors.New("object store restore input is invalid")
	}
	if _, err := application.repository.ObjectStore(ctx, input.StoreID); err != nil {
		return "", err
	}
	snapshot, err := decodeBackupSnapshot(input.Metadata, input.StoreID)
	if err != nil {
		return "", err
	}
	if err := input.ValidateAttachments(snapshot.Attachments); err != nil {
		return "", err
	}
	releaseExclusion, err := application.beginBackupExclusion(input.StoreID)
	if err != nil {
		return "", err
	}
	defer releaseExclusion()
	releaseMetadata, err := application.blockMetadataForRestore(ctx, input.StoreID)
	if err != nil {
		return "", err
	}
	defer releaseMetadata()

	attachmentsByPayload := make(map[string][]BackupAttachment, len(snapshot.Payloads))
	for _, attachment := range snapshot.Attachments {
		attachmentsByPayload[attachment.PayloadID] = append(
			attachmentsByPayload[attachment.PayloadID], attachment,
		)
	}
	for _, payload := range snapshot.Payloads {
		if err := application.payloads.InstallBackupPayload(
			ctx, input.StoreID, payload, attachmentsByPayload[payload.ID], input.OpenAttachment,
		); err != nil {
			return "", err
		}
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return "", err
	}
	payloads := make([]state.ObjectPayload, len(snapshot.Payloads))
	for index, payload := range snapshot.Payloads {
		payloads[index] = state.ObjectPayload{
			ID: payload.ID, ObjectStoreID: input.StoreID, PlaintextSize: payload.PlaintextSize,
			ChunkCount: payload.ChunkCount, PlaintextSHA256: payload.PlaintextSHA256,
			CreatedAtMillis: payload.CreatedAtMillis,
		}
	}
	objects := make([]state.ObjectMetadata, len(snapshot.Objects))
	for index, object := range snapshot.Objects {
		objects[index] = state.ObjectMetadata{
			ObjectStoreID: input.StoreID, ObjectKey: object.Key, PayloadID: object.PayloadID,
			ContentType: object.ContentType, ETag: object.ETag, Size: object.Size,
			CreatedAtMillis: object.CreatedAtMillis, UpdatedAtMillis: object.UpdatedAtMillis,
		}
	}
	err = application.repository.RestoreObjectStore(ctx, state.RestoreObjectStore{
		ObjectStoreID: input.StoreID, Payloads: payloads, Objects: objects,
		AuditEventID: identifiers[0], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[1],
		CreatedAtMillis: timestamp.UnixMilli(),
	})
	return identifiers[1], err
}

func decodeBackupSnapshot(value []byte, storeID string) (BackupSnapshot, error) {
	if len(value) == 0 || !safeComponent(storeID) {
		return BackupSnapshot{}, errors.New("object backup snapshot input is invalid")
	}
	if err := strictjson.RejectDuplicateKeys(value); err != nil {
		return BackupSnapshot{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var snapshot BackupSnapshot
	if err := decoder.Decode(&snapshot); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return BackupSnapshot{}, errors.New("object backup snapshot JSON is invalid")
	}
	if err := validateBackupSnapshot(snapshot, storeID); err != nil {
		return BackupSnapshot{}, err
	}
	return snapshot, nil
}

func validateBackupSnapshot(snapshot BackupSnapshot, storeID string) error {
	if snapshot.FormatVersion != BackupFormatVersion || snapshot.StoreID != storeID {
		return errors.New("object backup snapshot identity is invalid")
	}
	payloads := make(map[string]BackupPayload, len(snapshot.Payloads))
	for _, payload := range snapshot.Payloads {
		if !safeComponent(payload.ID) || payload.PlaintextSize < 0 || payload.PlaintextSize > MaximumObjectSize ||
			payload.ChunkCount < 0 || !validBackupSHA256(payload.PlaintextSHA256) || payload.CreatedAtMillis <= 0 {
			return errors.New("object backup payload metadata is invalid")
		}
		expectedChunks := int64(0)
		if payload.PlaintextSize > 0 {
			expectedChunks = (payload.PlaintextSize + ChunkSize - 1) / ChunkSize
		}
		if int64(payload.ChunkCount) != expectedChunks {
			return errors.New("object backup payload chunk count differs from size")
		}
		if _, exists := payloads[payload.ID]; exists {
			return errors.New("object backup payload IDs are duplicated")
		}
		payloads[payload.ID] = payload
	}

	referencedPayloads := make(map[string]struct{}, len(payloads))
	previousKey := ""
	for _, object := range snapshot.Objects {
		payload, exists := payloads[object.PayloadID]
		if validateObjectKey(object.Key) != nil || (previousKey != "" && object.Key <= previousKey) ||
			!exists || object.Size != payload.PlaintextSize || object.ETag != `"`+payload.PlaintextSHA256+`"` ||
			object.CreatedAtMillis <= 0 || object.UpdatedAtMillis < object.CreatedAtMillis ||
			!utf8.ValidString(object.ContentType) || strings.ContainsRune(object.ContentType, 0) {
			return errors.New("object backup object metadata is invalid or not canonically sorted")
		}
		previousKey = object.Key
		referencedPayloads[object.PayloadID] = struct{}{}
	}
	if len(referencedPayloads) != len(payloads) {
		return errors.New("object backup contains unreferenced payloads")
	}

	seenChunks := make(map[string]map[int]struct{}, len(payloads))
	for index, attachment := range snapshot.Attachments {
		payload, exists := payloads[attachment.PayloadID]
		if attachment.Index != index || !exists || attachment.ChunkIndex < 0 ||
			attachment.ChunkIndex >= payload.ChunkCount || attachment.Size <= 0 ||
			!validBackupSHA256(attachment.SHA256) {
			return errors.New("object backup attachment metadata is invalid")
		}
		plainSize := ChunkSize
		if attachment.ChunkIndex == payload.ChunkCount-1 {
			plainSize = int(payload.PlaintextSize - int64(attachment.ChunkIndex)*ChunkSize)
		}
		expectedSize := int64(chacha20poly1305.NonceSizeX + plainSize + chacha20poly1305.Overhead)
		if attachment.Size != expectedSize {
			return errors.New("object backup attachment encrypted size is invalid")
		}
		chunks := seenChunks[attachment.PayloadID]
		if chunks == nil {
			chunks = make(map[int]struct{}, payload.ChunkCount)
			seenChunks[attachment.PayloadID] = chunks
		}
		if _, duplicate := chunks[attachment.ChunkIndex]; duplicate {
			return errors.New("object backup attachment chunk is duplicated")
		}
		chunks[attachment.ChunkIndex] = struct{}{}
	}
	for payloadID, payload := range payloads {
		if len(seenChunks[payloadID]) != payload.ChunkCount {
			return errors.New("object backup payload attachment set is incomplete")
		}
	}
	return nil
}

func validBackupSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
