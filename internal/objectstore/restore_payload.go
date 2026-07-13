package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
)

type BackupAttachmentOpener func(context.Context, BackupAttachment) (io.ReadCloser, error)

func (store *PayloadStore) InstallBackupPayload(
	ctx context.Context,
	storeID string,
	payload BackupPayload,
	attachments []BackupAttachment,
	open BackupAttachmentOpener,
) error {
	if ctx == nil || !safeComponent(storeID) || !safeComponent(payload.ID) ||
		len(attachments) != payload.ChunkCount || (len(attachments) > 0 && open == nil) {
		return errors.New("object backup payload install input is invalid")
	}
	payloadRoot, err := store.payloadRoot(storeID)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(payloadRoot, ".restore-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	for chunkIndex, attachment := range attachments {
		if attachment.PayloadID != payload.ID || attachment.ChunkIndex != chunkIndex {
			return errors.New("object backup payload attachments are out of order")
		}
		reader, err := open(ctx, attachment)
		if err != nil {
			return err
		}
		path := filepath.Join(temporary, chunkName(chunkIndex))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			return err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(file, hash), &backupContextReader{ctx: ctx, source: reader})
		if copyErr == nil && (written != attachment.Size || hex.EncodeToString(hash.Sum(nil)) != attachment.SHA256) {
			copyErr = errors.New("object backup attachment differs from metadata")
		}
		if copyErr == nil {
			copyErr = file.Sync()
		}
		fileCloseErr := file.Close()
		readerCloseErr := reader.Close()
		if copyErr != nil || fileCloseErr != nil || readerCloseErr != nil {
			return errors.Join(copyErr, fileCloseErr, readerCloseErr)
		}
	}
	info := PayloadInfo{
		ID: payload.ID, PlaintextSize: payload.PlaintextSize,
		ChunkCount: payload.ChunkCount, PlaintextSHA256: payload.PlaintextSHA256,
	}
	if err := store.verifyBackupPayloadDirectory(ctx, temporary, storeID, info); err != nil {
		return err
	}
	finalPath := filepath.Join(payloadRoot, payload.ID)
	if existing, statErr := os.Lstat(finalPath); statErr == nil {
		if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
			return errors.New("existing object payload path is not a directory")
		}
		// Payload IDs are immutable. Existing bytes may use different random
		// nonces, so semantic AEAD/plaintext verification is stronger than a
		// byte-for-byte comparison and avoids disrupting concurrent GETs.
		if err := store.verifyBackupPayloadDirectory(ctx, finalPath, storeID, info); err != nil {
			return fmt.Errorf("verify existing object payload: %w", err)
		}
		if err := os.RemoveAll(temporary); err != nil {
			return err
		}
		committed = true
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.Rename(temporary, finalPath); err != nil {
		return err
	}
	committed = true
	return syncDirectory(payloadRoot)
}

func (store *PayloadStore) verifyBackupPayloadDirectory(
	ctx context.Context,
	root, storeID string,
	payload PayloadInfo,
) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != payload.ChunkCount {
		return errors.New("object backup payload directory has unexpected entries")
	}
	key, err := deriveStoreKey(store.master, storeID)
	if err != nil {
		return err
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return err
	}
	plaintextHash := sha256.New()
	var plaintextTotal int64
	for chunkIndex := 0; chunkIndex < payload.ChunkCount; chunkIndex++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		plainSize, err := restoredChunkPlaintextSize(payload, chunkIndex)
		if err != nil {
			return err
		}
		path := filepath.Join(root, chunkName(chunkIndex))
		pathInfo, err := os.Lstat(path)
		if err != nil || !pathInfo.Mode().IsRegular() {
			return errors.Join(err, errors.New("object backup chunk is not a regular file"))
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expectedSize := chacha20poly1305.NonceSizeX + plainSize + aead.Overhead()
		if len(encoded) != expectedSize {
			clear(encoded)
			return errors.New("object backup encrypted chunk size is invalid")
		}
		plaintext, err := aead.Open(
			nil,
			encoded[:chacha20poly1305.NonceSizeX],
			encoded[chacha20poly1305.NonceSizeX:],
			payloadAdditionalData(storeID, payload.ID, chunkIndex, plainSize),
		)
		clear(encoded)
		if err != nil {
			return fmt.Errorf("authenticate restored object chunk %d: %w", chunkIndex, err)
		}
		_, _ = plaintextHash.Write(plaintext)
		plaintextTotal += int64(len(plaintext))
		clear(plaintext)
	}
	if plaintextTotal != payload.PlaintextSize || hex.EncodeToString(plaintextHash.Sum(nil)) != payload.PlaintextSHA256 {
		return errors.New("object backup payload plaintext differs from metadata")
	}
	return nil
}

func restoredChunkPlaintextSize(payload PayloadInfo, chunkIndex int) (int, error) {
	if payload.PlaintextSize < 0 || payload.ChunkCount < 0 || chunkIndex < 0 || chunkIndex >= payload.ChunkCount {
		return 0, errors.New("object backup payload chunk bounds are invalid")
	}
	plainSize := ChunkSize
	if chunkIndex == payload.ChunkCount-1 {
		plainSize = int(payload.PlaintextSize - int64(chunkIndex)*ChunkSize)
	}
	if plainSize <= 0 || plainSize > ChunkSize {
		return 0, errors.New("object backup payload chunk size is invalid")
	}
	return plainSize, nil
}
