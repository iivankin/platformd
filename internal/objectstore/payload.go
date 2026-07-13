package objectstore

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	ChunkSize            = 4 << 20
	MaximumObjectSize    = int64(100) << 30
	storeKeyDomain       = "platformd/s3/store/v1"
	payloadFormatVersion = byte(1)
)

type PayloadInfo struct {
	ID              string
	PlaintextSize   int64
	ChunkCount      int
	PlaintextSHA256 string
}

type BackupChunkInfo struct {
	Path   string
	Size   int64
	SHA256 string
}

type PayloadStore struct {
	root   string
	master cryptobox.MasterKey
	random io.Reader
}

func NewPayloadStore(root string, master cryptobox.MasterKey, random io.Reader) (*PayloadStore, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, errors.New("object payload root must be a canonical absolute non-root path")
	}
	if random == nil {
		random = rand.Reader
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &PayloadStore{root: root, master: master, random: random}, nil
}

func (store *PayloadStore) Write(ctx context.Context, storeID, payloadID string, input io.Reader) (PayloadInfo, error) {
	if !safeComponent(storeID) || !safeComponent(payloadID) || input == nil {
		return PayloadInfo{}, errors.New("object payload write input is invalid")
	}
	payloadRoot, err := store.payloadRoot(storeID)
	if err != nil {
		return PayloadInfo{}, err
	}
	temporary, err := os.MkdirTemp(payloadRoot, ".payload-")
	if err != nil {
		return PayloadInfo{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	key, err := deriveStoreKey(store.master, storeID)
	if err != nil {
		return PayloadInfo{}, err
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return PayloadInfo{}, err
	}
	buffer := make([]byte, ChunkSize)
	defer clear(buffer)
	hash := sha256.New()
	var total int64
	chunks := 0
	for {
		if err := ctx.Err(); err != nil {
			return PayloadInfo{}, err
		}
		count, readErr := io.ReadFull(input, buffer)
		if errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.EOF) {
			readErr = nil
		}
		if readErr != nil {
			return PayloadInfo{}, readErr
		}
		if count == 0 {
			break
		}
		if total+int64(count) > MaximumObjectSize {
			return PayloadInfo{}, errors.New("object exceeds 100 GiB")
		}
		plaintext := buffer[:count]
		_, _ = hash.Write(plaintext)
		nonce := make([]byte, chacha20poly1305.NonceSizeX)
		if _, err := io.ReadFull(store.random, nonce); err != nil {
			return PayloadInfo{}, err
		}
		ciphertext := aead.Seal(nil, nonce, plaintext, payloadAdditionalData(storeID, payloadID, chunks, count))
		path := filepath.Join(temporary, chunkName(chunks))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return PayloadInfo{}, err
		}
		if _, err = file.Write(nonce); err == nil {
			_, err = file.Write(ciphertext)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return PayloadInfo{}, errors.Join(err, closeErr)
		}
		total += int64(count)
		chunks++
		if count < ChunkSize {
			break
		}
	}
	finalPath := filepath.Join(payloadRoot, payloadID)
	if _, err := os.Lstat(finalPath); err == nil {
		return PayloadInfo{}, errors.New("object payload ID already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PayloadInfo{}, err
	}
	if err := os.Rename(temporary, finalPath); err != nil {
		return PayloadInfo{}, err
	}
	committed = true
	if err := syncDirectory(payloadRoot); err != nil {
		return PayloadInfo{}, err
	}
	return PayloadInfo{
		ID: payloadID, PlaintextSize: total, ChunkCount: chunks,
		PlaintextSHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func (store *PayloadStore) ReadRange(ctx context.Context, storeID string, payload PayloadInfo, offset, length int64, output io.Writer) error {
	if !safeComponent(storeID) || !safeComponent(payload.ID) || offset < 0 || length < 0 || offset > payload.PlaintextSize || length > payload.PlaintextSize-offset || output == nil {
		return errors.New("object payload range is invalid")
	}
	if length == 0 {
		return nil
	}
	key, err := deriveStoreKey(store.master, storeID)
	if err != nil {
		return err
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return err
	}
	firstChunk := int(offset / ChunkSize)
	lastChunk := int((offset + length - 1) / ChunkSize)
	remaining := length
	for chunkIndex := firstChunk; chunkIndex <= lastChunk; chunkIndex++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		plainSize := ChunkSize
		if chunkIndex == payload.ChunkCount-1 {
			plainSize = int(payload.PlaintextSize - int64(chunkIndex)*ChunkSize)
		}
		path := filepath.Join(store.root, storeID, "payloads", payload.ID, chunkName(chunkIndex))
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expected := chacha20poly1305.NonceSizeX + plainSize + aead.Overhead()
		if len(encoded) != expected {
			return errors.New("encrypted object chunk size is invalid")
		}
		nonce := encoded[:chacha20poly1305.NonceSizeX]
		plaintext, err := aead.Open(nil, nonce, encoded[chacha20poly1305.NonceSizeX:], payloadAdditionalData(storeID, payload.ID, chunkIndex, plainSize))
		if err != nil {
			return fmt.Errorf("authenticate object chunk %d: %w", chunkIndex, err)
		}
		start := 0
		if chunkIndex == firstChunk {
			start = int(offset % ChunkSize)
		}
		count := min(int64(len(plaintext)-start), remaining)
		if _, err := output.Write(plaintext[start : start+int(count)]); err != nil {
			clear(plaintext)
			return err
		}
		clear(plaintext)
		remaining -= count
	}
	if remaining != 0 {
		return errors.New("object payload range ended early")
	}
	return nil
}

func (store *PayloadStore) Delete(storeID, payloadID string) error {
	if !safeComponent(storeID) || !safeComponent(payloadID) {
		return errors.New("object payload delete input is invalid")
	}
	return os.RemoveAll(filepath.Join(store.root, storeID, "payloads", payloadID))
}

func (store *PayloadStore) BackupChunk(ctx context.Context, storeID, payloadID string, chunkIndex int) (BackupChunkInfo, error) {
	if !safeComponent(storeID) || !safeComponent(payloadID) || chunkIndex < 0 {
		return BackupChunkInfo{}, errors.New("object backup chunk identity is invalid")
	}
	path := filepath.Join(store.root, storeID, "payloads", payloadID, chunkName(chunkIndex))
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 {
		return BackupChunkInfo{}, errors.Join(err, errors.New("object backup chunk is empty or not a regular file"))
	}
	file, err := os.Open(path)
	if err != nil {
		return BackupChunkInfo{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, &backupContextReader{ctx: ctx, source: file})
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != pathInfo.Size() {
		return BackupChunkInfo{}, errors.Join(copyErr, closeErr, errors.New("object backup chunk changed while hashing"))
	}
	return BackupChunkInfo{Path: path, Size: written, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
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

func (store *PayloadStore) payloadRoot(storeID string) (string, error) {
	root := filepath.Join(store.root, storeID)
	if err := os.MkdirAll(filepath.Join(root, "payloads"), 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "multipart"), 0o700); err != nil {
		return "", err
	}
	return filepath.Join(root, "payloads"), nil
}

func deriveStoreKey(master cryptobox.MasterKey, storeID string) ([32]byte, error) {
	value, err := hkdf.Key(sha256.New, master[:], []byte(storeID), storeKeyDomain, 32)
	if err != nil {
		return [32]byte{}, err
	}
	var key [32]byte
	copy(key[:], value)
	clear(value)
	return key, nil
}

func payloadAdditionalData(storeID, payloadID string, chunkIndex, plaintextSize int) []byte {
	value := make([]byte, 0, 2+len(storeID)+len(payloadID)+16)
	value = append(value, payloadFormatVersion, 0)
	value = append(value, storeID...)
	value = append(value, 0)
	value = append(value, payloadID...)
	value = append(value, 0)
	var numbers [16]byte
	binary.BigEndian.PutUint64(numbers[:8], uint64(chunkIndex))
	binary.BigEndian.PutUint64(numbers[8:], uint64(plaintextSize))
	return append(value, numbers[:]...)
}

func chunkName(index int) string {
	return strconv.FormatInt(int64(index), 10) + ".chunk"
}

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\\x00")
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
