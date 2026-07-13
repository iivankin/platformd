package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	MinimumMultipartPartSize = int64(5) << 20
	MaximumMultipartPartSize = int64(512) << 20
)

type MultipartPartInfo struct {
	PartNumber     int
	PlaintextSize  int64
	ChunkCount     int
	ChecksumSHA256 string
}

func (store *PayloadStore) WriteMultipartPart(ctx context.Context, storeID, uploadID string, partNumber int, input io.Reader) (MultipartPartInfo, error) {
	if !safeComponent(storeID) || !safeComponent(uploadID) || partNumber < 1 || partNumber > 10_000 || input == nil {
		return MultipartPartInfo{}, errors.New("multipart payload write input is invalid")
	}
	root := filepath.Join(store.root, storeID, "multipart", uploadID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return MultipartPartInfo{}, err
	}
	temporary, err := os.MkdirTemp(root, ".part-")
	if err != nil {
		return MultipartPartInfo{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	key, err := deriveStoreKey(store.master, storeID)
	if err != nil {
		return MultipartPartInfo{}, err
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return MultipartPartInfo{}, err
	}
	buffer := make([]byte, ChunkSize)
	defer clear(buffer)
	hash := sha256.New()
	var total int64
	chunks := 0
	for {
		if err := ctx.Err(); err != nil {
			return MultipartPartInfo{}, err
		}
		count, readErr := io.ReadFull(input, buffer)
		if errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.EOF) {
			readErr = nil
		}
		if readErr != nil {
			return MultipartPartInfo{}, readErr
		}
		if count == 0 {
			break
		}
		if total+int64(count) > MaximumMultipartPartSize {
			return MultipartPartInfo{}, errors.New("multipart part exceeds 512 MiB")
		}
		plaintext := buffer[:count]
		_, _ = hash.Write(plaintext)
		nonce := make([]byte, chacha20poly1305.NonceSizeX)
		if _, err := io.ReadFull(store.random, nonce); err != nil {
			return MultipartPartInfo{}, err
		}
		ciphertext := aead.Seal(nil, nonce, plaintext, multipartAdditionalData(storeID, uploadID, partNumber, chunks, count))
		file, err := os.OpenFile(filepath.Join(temporary, chunkName(chunks)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return MultipartPartInfo{}, err
		}
		if _, err = file.Write(nonce); err == nil {
			_, err = file.Write(ciphertext)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return MultipartPartInfo{}, errors.Join(err, closeErr)
		}
		total += int64(count)
		chunks++
		if count < ChunkSize {
			break
		}
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	finalPath := multipartPartPath(root, partNumber, checksum)
	if _, err := os.Lstat(finalPath); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporary, finalPath); err != nil {
			return MultipartPartInfo{}, err
		}
		committed = true
		if err := syncDirectory(root); err != nil {
			return MultipartPartInfo{}, err
		}
	} else if err != nil {
		return MultipartPartInfo{}, err
	}
	return MultipartPartInfo{
		PartNumber: partNumber, PlaintextSize: total, ChunkCount: chunks,
		ChecksumSHA256: checksum,
	}, nil
}

func (store *PayloadStore) ReadMultipartPart(ctx context.Context, storeID, uploadID string, part MultipartPartInfo, output io.Writer) error {
	if !safeComponent(storeID) || !safeComponent(uploadID) || part.PartNumber < 1 || part.PartNumber > 10_000 || part.PlaintextSize < 0 || part.ChunkCount < 0 || !validSHA256(part.ChecksumSHA256) || output == nil {
		return errors.New("multipart payload read input is invalid")
	}
	key, err := deriveStoreKey(store.master, storeID)
	if err != nil {
		return err
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return err
	}
	root := filepath.Join(store.root, storeID, "multipart", uploadID)
	hash := sha256.New()
	for chunkIndex := 0; chunkIndex < part.ChunkCount; chunkIndex++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		plainSize := ChunkSize
		if chunkIndex == part.ChunkCount-1 {
			plainSize = int(part.PlaintextSize - int64(chunkIndex)*ChunkSize)
		}
		encoded, err := os.ReadFile(filepath.Join(multipartPartPath(root, part.PartNumber, part.ChecksumSHA256), chunkName(chunkIndex)))
		if err != nil {
			return err
		}
		expected := chacha20poly1305.NonceSizeX + plainSize + aead.Overhead()
		if len(encoded) != expected {
			return errors.New("encrypted multipart chunk size is invalid")
		}
		nonce := encoded[:chacha20poly1305.NonceSizeX]
		plaintext, err := aead.Open(nil, nonce, encoded[chacha20poly1305.NonceSizeX:], multipartAdditionalData(storeID, uploadID, part.PartNumber, chunkIndex, plainSize))
		if err != nil {
			return fmt.Errorf("authenticate multipart chunk %d: %w", chunkIndex, err)
		}
		_, _ = hash.Write(plaintext)
		if _, err := output.Write(plaintext); err != nil {
			clear(plaintext)
			return err
		}
		clear(plaintext)
	}
	if hex.EncodeToString(hash.Sum(nil)) != part.ChecksumSHA256 {
		return errors.New("multipart part checksum is invalid")
	}
	return nil
}

func (store *PayloadStore) DeleteMultipart(storeID, uploadID string) error {
	if !safeComponent(storeID) || !safeComponent(uploadID) {
		return errors.New("multipart payload delete input is invalid")
	}
	return os.RemoveAll(filepath.Join(store.root, storeID, "multipart", uploadID))
}

func multipartPartPath(root string, partNumber int, checksum string) string {
	return filepath.Join(root, strconv.Itoa(partNumber)+"-"+checksum)
}

func multipartAdditionalData(storeID, uploadID string, partNumber, chunkIndex, plaintextSize int) []byte {
	value := make([]byte, 0, 3+len(storeID)+len(uploadID)+24)
	value = append(value, payloadFormatVersion, 1, 0)
	value = append(value, storeID...)
	value = append(value, 0)
	value = append(value, uploadID...)
	value = append(value, 0)
	var numbers [24]byte
	binary.BigEndian.PutUint64(numbers[:8], uint64(partNumber))
	binary.BigEndian.PutUint64(numbers[8:16], uint64(chunkIndex))
	binary.BigEndian.PutUint64(numbers[16:], uint64(plaintextSize))
	return append(value, numbers[:]...)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
