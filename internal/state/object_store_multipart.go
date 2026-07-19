package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrMultipartUploadNotFound = errors.New("multipart upload not found")

type MultipartUpload struct {
	ID              string
	ObjectStoreID   string
	ObjectKey       string
	ContentType     string
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

type MultipartPart struct {
	UploadID       string
	PartNumber     int
	PlaintextSize  int64
	ChecksumSHA256 string
	ChunkCount     int
}

type CreateMultipartUpload struct {
	ID              string
	ObjectStoreID   string
	ObjectKey       string
	ContentType     string
	CreatedAtMillis int64
	ExpiresAtMillis int64
}

func (store *Store) CreateMultipartUpload(ctx context.Context, input CreateMultipartUpload) (MultipartUpload, error) {
	if input.ID == "" || input.ObjectStoreID == "" || input.ObjectKey == "" || input.CreatedAtMillis <= 0 || input.ExpiresAtMillis <= input.CreatedAtMillis {
		return MultipartUpload{}, errors.New("create multipart upload input is invalid")
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM object_stores WHERE id = ?)", input.ObjectStoreID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrObjectStoreNotFound
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO multipart_uploads(id, object_store_id, object_key, content_type, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.ID, input.ObjectStoreID, input.ObjectKey,
			nullableString(input.ContentType), input.CreatedAtMillis, input.ExpiresAtMillis)
		return err
	})
	if err != nil {
		return MultipartUpload{}, err
	}
	return store.MultipartUpload(ctx, input.ObjectStoreID, input.ID, input.ObjectKey)
}

func (store *Store) MultipartUpload(ctx context.Context, storeID, uploadID, objectKey string) (MultipartUpload, error) {
	var result MultipartUpload
	var contentType sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT id, object_store_id, object_key, content_type, created_at, expires_at
FROM multipart_uploads WHERE id = ? AND object_store_id = ? AND object_key = ?`,
		uploadID, storeID, objectKey).Scan(
		&result.ID, &result.ObjectStoreID, &result.ObjectKey, &contentType,
		&result.CreatedAtMillis, &result.ExpiresAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MultipartUpload{}, ErrMultipartUploadNotFound
	}
	if err != nil {
		return MultipartUpload{}, err
	}
	result.ContentType = contentType.String
	return result, nil
}

func (store *Store) CommitMultipartPart(ctx context.Context, storeID, uploadID, objectKey string, part MultipartPart, nowMillis int64) error {
	if part.UploadID != uploadID || part.PartNumber < 1 || part.PartNumber > 10_000 || part.PlaintextSize < 0 || part.ChecksumSHA256 == "" || part.ChunkCount < 0 || nowMillis <= 0 {
		return errors.New("multipart part input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		var expiresAt int64
		err := transaction.QueryRowContext(ctx, `
SELECT expires_at FROM multipart_uploads
WHERE id = ? AND object_store_id = ? AND object_key = ?`, uploadID, storeID, objectKey).Scan(&expiresAt)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && nowMillis >= expiresAt) {
			return ErrMultipartUploadNotFound
		}
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO multipart_parts(upload_id, part_number, plaintext_size, checksum_sha256, chunk_count)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(upload_id, part_number) DO UPDATE SET
  plaintext_size = excluded.plaintext_size,
  checksum_sha256 = excluded.checksum_sha256,
  chunk_count = excluded.chunk_count`, uploadID, part.PartNumber, part.PlaintextSize,
			part.ChecksumSHA256, part.ChunkCount)
		return err
	})
}

func (store *Store) MultipartPart(ctx context.Context, storeID, uploadID, objectKey string, partNumber int) (MultipartPart, error) {
	var result MultipartPart
	err := store.database.QueryRowContext(ctx, `
SELECT p.upload_id, p.part_number, p.plaintext_size, p.checksum_sha256, p.chunk_count
FROM multipart_parts p JOIN multipart_uploads u ON u.id = p.upload_id
WHERE u.object_store_id = ? AND u.id = ? AND u.object_key = ? AND p.part_number = ?`,
		storeID, uploadID, objectKey, partNumber).Scan(
		&result.UploadID, &result.PartNumber, &result.PlaintextSize,
		&result.ChecksumSHA256, &result.ChunkCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MultipartPart{}, ErrMultipartUploadNotFound
	}
	return result, err
}

func (store *Store) MultipartParts(ctx context.Context, storeID, uploadID, objectKey string, after, limit int) ([]MultipartPart, bool, error) {
	if after < 0 || after > 10_000 || limit < 1 || limit > 1000 {
		return nil, false, errors.New("multipart part list bounds are invalid")
	}
	if _, err := store.MultipartUpload(ctx, storeID, uploadID, objectKey); err != nil {
		return nil, false, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT upload_id, part_number, plaintext_size, checksum_sha256, chunk_count
FROM multipart_parts WHERE upload_id = ? AND part_number > ?
ORDER BY part_number LIMIT ?`, uploadID, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]MultipartPart, 0, limit)
	for rows.Next() {
		var part MultipartPart
		if err := rows.Scan(&part.UploadID, &part.PartNumber, &part.PlaintextSize, &part.ChecksumSHA256, &part.ChunkCount); err != nil {
			return nil, false, err
		}
		result = append(result, part)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

type CompleteMultipartPart struct {
	PartNumber     int
	ChecksumSHA256 string
}

type CompleteMultipartUpload struct {
	ObjectStoreID     string
	UploadID          string
	ObjectKey         string
	Parts             []CompleteMultipartPart
	Payload           ObjectPayload
	ContentType       string
	ETag              string
	CompletedAtMillis int64
}

func (store *Store) CompleteMultipartUpload(ctx context.Context, input CompleteMultipartUpload) (ObjectMetadata, error) {
	if input.ObjectStoreID == "" || input.UploadID == "" || input.ObjectKey == "" || len(input.Parts) == 0 || input.Payload.ID == "" || input.Payload.ObjectStoreID != input.ObjectStoreID || input.ETag == "" || input.CompletedAtMillis <= 0 {
		return ObjectMetadata{}, errors.New("complete multipart upload input is invalid")
	}
	err := store.Write(ctx, func(transaction *sql.Tx) error {
		var expiresAt int64
		err := transaction.QueryRowContext(ctx, `
SELECT expires_at FROM multipart_uploads
WHERE id = ? AND object_store_id = ? AND object_key = ?`, input.UploadID, input.ObjectStoreID, input.ObjectKey).Scan(&expiresAt)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && input.CompletedAtMillis >= expiresAt) {
			return ErrMultipartUploadNotFound
		}
		if err != nil {
			return err
		}
		for _, expected := range input.Parts {
			var checksum string
			if err := transaction.QueryRowContext(ctx, `
SELECT checksum_sha256 FROM multipart_parts WHERE upload_id = ? AND part_number = ?`,
				input.UploadID, expected.PartNumber).Scan(&checksum); errors.Is(err, sql.ErrNoRows) {
				return ErrMultipartUploadNotFound
			} else if err != nil {
				return err
			}
			if checksum != expected.ChecksumSHA256 {
				return ErrMultipartUploadNotFound
			}
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO object_payloads(id, object_store_id, plaintext_size, chunk_count, plaintext_sha256, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, input.Payload.ID, input.ObjectStoreID, input.Payload.PlaintextSize,
			input.Payload.ChunkCount, input.Payload.PlaintextSHA256, input.CompletedAtMillis); err != nil {
			return fmt.Errorf("create multipart final payload metadata: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO objects(object_store_id, object_key, payload_id, content_type, etag, size, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(object_store_id, object_key) DO UPDATE SET
  payload_id = excluded.payload_id, content_type = excluded.content_type,
  etag = excluded.etag, size = excluded.size, updated_at = excluded.updated_at`,
			input.ObjectStoreID, input.ObjectKey, input.Payload.ID, nullableString(input.ContentType),
			input.ETag, input.Payload.PlaintextSize, input.CompletedAtMillis, input.CompletedAtMillis); err != nil {
			return fmt.Errorf("publish multipart object metadata: %w", err)
		}
		result, err := transaction.ExecContext(ctx, "DELETE FROM multipart_uploads WHERE id = ?", input.UploadID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil || deleted != 1 {
			return errors.Join(err, ErrMultipartUploadNotFound)
		}
		return nil
	})
	if err != nil {
		return ObjectMetadata{}, err
	}
	return store.Object(ctx, input.ObjectStoreID, input.ObjectKey)
}

func (store *Store) AbortMultipartUpload(ctx context.Context, storeID, uploadID, objectKey string) error {
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM multipart_uploads WHERE id = ? AND object_store_id = ? AND object_key = ?`,
			uploadID, storeID, objectKey)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return ErrMultipartUploadNotFound
		}
		return nil
	})
}

func (store *Store) ExpiredMultipartUploads(ctx context.Context, beforeMillis int64, limit int) ([]MultipartUpload, error) {
	if beforeMillis <= 0 || limit < 1 || limit > 1000 {
		return nil, errors.New("expired multipart upload query is invalid")
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, object_store_id, object_key, content_type, created_at, expires_at
FROM multipart_uploads
WHERE expires_at <= ?
ORDER BY expires_at, id
LIMIT ?`, beforeMillis, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired multipart uploads: %w", err)
	}
	defer rows.Close()
	result := make([]MultipartUpload, 0, limit)
	for rows.Next() {
		var upload MultipartUpload
		var contentType sql.NullString
		if err := rows.Scan(
			&upload.ID, &upload.ObjectStoreID, &upload.ObjectKey, &contentType,
			&upload.CreatedAtMillis, &upload.ExpiresAtMillis,
		); err != nil {
			return nil, fmt.Errorf("scan expired multipart upload: %w", err)
		}
		upload.ContentType = contentType.String
		result = append(result, upload)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired multipart uploads: %w", err)
	}
	return result, nil
}
