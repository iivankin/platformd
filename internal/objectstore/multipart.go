package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const MultipartUploadTTL = 24 * time.Hour

type CreateMultipartResult struct {
	Upload state.MultipartUpload
}

type CompletedPart struct {
	PartNumber int
	ETag       string
}

func (application *Application) CreateMultipart(ctx context.Context, storeID, objectKey, contentType string) (CreateMultipartResult, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return CreateMultipartResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	now := application.now()
	uploadID, err := id.NewWith(now, application.random)
	if err != nil {
		return CreateMultipartResult{}, err
	}
	upload, err := application.repository.CreateMultipartUpload(ctx, state.CreateMultipartUpload{
		ID: uploadID, ObjectStoreID: storeID, ObjectKey: objectKey, ContentType: contentType,
		CreatedAtMillis: now.UnixMilli(), ExpiresAtMillis: now.Add(MultipartUploadTTL).UnixMilli(),
	})
	return CreateMultipartResult{Upload: upload}, err
}

func (application *Application) UploadPart(ctx context.Context, storeID, uploadID, objectKey string, partNumber int, expectedSHA256 string, body io.Reader) (state.MultipartPart, error) {
	if err := validateObjectKey(objectKey); err != nil || !safeComponent(uploadID) || partNumber < 1 || partNumber > 10_000 || body == nil {
		return state.MultipartPart{}, fmt.Errorf("%w: invalid multipart part input", ErrInvalidInput)
	}
	if _, err := application.repository.MultipartUpload(ctx, storeID, uploadID, objectKey); err != nil {
		return state.MultipartPart{}, err
	}
	part, err := application.payloads.WriteMultipartPart(ctx, storeID, uploadID, partNumber, body)
	if err != nil {
		return state.MultipartPart{}, err
	}
	if expectedSHA256 != "" && !strings.EqualFold(expectedSHA256, part.ChecksumSHA256) {
		return state.MultipartPart{}, ErrBadDigest
	}
	result := state.MultipartPart{
		UploadID: uploadID, PartNumber: part.PartNumber, PlaintextSize: part.PlaintextSize,
		ChecksumSHA256: part.ChecksumSHA256, ChunkCount: part.ChunkCount,
	}
	if err := application.repository.CommitMultipartPart(ctx, storeID, uploadID, objectKey, result, application.now().UnixMilli()); err != nil {
		return state.MultipartPart{}, err
	}
	return result, nil
}

func (application *Application) Parts(ctx context.Context, storeID, uploadID, objectKey string, after, limit int) (state.MultipartUpload, []state.MultipartPart, bool, error) {
	upload, err := application.repository.MultipartUpload(ctx, storeID, uploadID, objectKey)
	if err != nil {
		return state.MultipartUpload{}, nil, false, err
	}
	parts, more, err := application.repository.MultipartParts(ctx, storeID, uploadID, objectKey, after, limit)
	return upload, parts, more, err
}

func (application *Application) CompleteMultipart(ctx context.Context, storeID, uploadID, objectKey string, requested []CompletedPart) (state.ObjectMetadata, error) {
	if err := validateCompletionInput(uploadID, objectKey, requested); err != nil {
		return state.ObjectMetadata{}, err
	}
	upload, err := application.repository.MultipartUpload(ctx, storeID, uploadID, objectKey)
	if err != nil {
		return state.ObjectMetadata{}, err
	}
	parts := make([]state.MultipartPart, len(requested))
	for index, expected := range requested {
		part, err := application.repository.MultipartPart(ctx, storeID, uploadID, objectKey, expected.PartNumber)
		if err != nil {
			return state.ObjectMetadata{}, err
		}
		checksum := strings.Trim(expected.ETag, `"`)
		if checksum != part.ChecksumSHA256 || (index < len(requested)-1 && part.PlaintextSize < MinimumMultipartPartSize) {
			return state.ObjectMetadata{}, fmt.Errorf("%w: multipart part size or ETag is invalid", ErrInvalidInput)
		}
		parts[index] = part
	}
	payloadID, err := id.NewWith(application.now(), application.random)
	if err != nil {
		return state.ObjectMetadata{}, err
	}
	reader, writer := io.Pipe()
	go application.streamMultipartParts(ctx, storeID, uploadID, parts, writer)
	payload, writeErr := application.payloads.Write(ctx, storeID, payloadID, reader)
	closeErr := reader.Close()
	if writeErr != nil || closeErr != nil {
		return state.ObjectMetadata{}, errors.Join(writeErr, closeErr)
	}
	nowMillis := application.now().UnixMilli()
	completion := make([]state.CompleteMultipartPart, len(parts))
	for index, part := range parts {
		completion[index] = state.CompleteMultipartPart{PartNumber: part.PartNumber, ChecksumSHA256: part.ChecksumSHA256}
	}
	finishMutation, err := application.beginMetadataMutation(ctx, storeID)
	if err != nil {
		_ = application.payloads.Delete(storeID, payload.ID)
		return state.ObjectMetadata{}, err
	}
	defer finishMutation()
	metadata, err := application.repository.CompleteMultipartUpload(ctx, state.CompleteMultipartUpload{
		ObjectStoreID: storeID, UploadID: uploadID, ObjectKey: objectKey, Parts: completion,
		Payload: state.ObjectPayload{
			ID: payload.ID, ObjectStoreID: storeID, PlaintextSize: payload.PlaintextSize,
			ChunkCount: payload.ChunkCount, PlaintextSHA256: payload.PlaintextSHA256, CreatedAtMillis: nowMillis,
		},
		ContentType: upload.ContentType, ETag: `"` + payload.PlaintextSHA256 + `"`, CompletedAtMillis: nowMillis,
	})
	if err != nil {
		return state.ObjectMetadata{}, err
	}
	_ = application.payloads.DeleteMultipart(storeID, uploadID)
	return metadata, nil
}

func (application *Application) streamMultipartParts(ctx context.Context, storeID, uploadID string, parts []state.MultipartPart, output *io.PipeWriter) {
	for _, part := range parts {
		err := application.payloads.ReadMultipartPart(ctx, storeID, uploadID, MultipartPartInfo{
			PartNumber: part.PartNumber, PlaintextSize: part.PlaintextSize,
			ChunkCount: part.ChunkCount, ChecksumSHA256: part.ChecksumSHA256,
		}, output)
		if err != nil {
			_ = output.CloseWithError(err)
			return
		}
	}
	_ = output.Close()
}

func (application *Application) AbortMultipart(ctx context.Context, storeID, uploadID, objectKey string) error {
	if !safeComponent(uploadID) || validateObjectKey(objectKey) != nil {
		return fmt.Errorf("%w: invalid multipart abort input", ErrInvalidInput)
	}
	if err := application.repository.AbortMultipartUpload(ctx, storeID, uploadID, objectKey); err != nil {
		return err
	}
	return application.payloads.DeleteMultipart(storeID, uploadID)
}

func validateCompletionInput(uploadID, objectKey string, parts []CompletedPart) error {
	if !safeComponent(uploadID) || validateObjectKey(objectKey) != nil || len(parts) < 1 || len(parts) > 10_000 {
		return fmt.Errorf("%w: invalid multipart completion", ErrInvalidInput)
	}
	previous := 0
	for _, part := range parts {
		checksum := strings.Trim(part.ETag, `"`)
		if part.PartNumber <= previous || part.PartNumber > 10_000 || !validSHA256(checksum) || part.ETag != `"`+checksum+`"` {
			return fmt.Errorf("%w: multipart parts must be ordered with exact quoted ETags", ErrInvalidInput)
		}
		previous = part.PartNumber
	}
	return nil
}
