package backupcrypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const (
	ResourceKeyDomain = "platformd/backup/v1"
	DefaultChunkSize  = 8 << 20
)

type Chunk struct {
	Index          int    `json:"index"`
	PlaintextSize  int    `json:"plaintextSize"`
	CiphertextSize int    `json:"ciphertextSize"`
	CiphertextSHA  string `json:"ciphertextSha256"`
}

type WorkChunk struct {
	Chunk
	Path string `json:"-"`
}

type ResourceCipher struct {
	resourceID string
	box        cryptobox.Box
}

func NewResourceCipher(master cryptobox.MasterKey, resourceID string) (*ResourceCipher, error) {
	if resourceID == "" {
		return nil, errors.New("backup resource ID is empty")
	}
	box, err := cryptobox.NewBox(master, []byte(resourceID), ResourceKeyDomain)
	if err != nil {
		return nil, err
	}
	return &ResourceCipher{resourceID: resourceID, box: box}, nil
}

func (cipher *ResourceCipher) SealChunk(generationID string, index int, plaintext []byte, random io.Reader) ([]byte, Chunk, error) {
	if generationID == "" || index < 0 || len(plaintext) > DefaultChunkSize {
		return nil, Chunk{}, errors.New("backup chunk identity or size is invalid")
	}
	if random == nil {
		random = rand.Reader
	}
	additionalData := chunkAdditionalData(cipher.resourceID, generationID, index, len(plaintext))
	sealed, err := cipher.box.SealWith(random, plaintext, additionalData)
	if err != nil {
		return nil, Chunk{}, err
	}
	checksum := sha256.Sum256(sealed)
	return sealed, Chunk{
		Index: index, PlaintextSize: len(plaintext), CiphertextSize: len(sealed),
		CiphertextSHA: hex.EncodeToString(checksum[:]),
	}, nil
}

func (cipher *ResourceCipher) OpenChunk(generationID string, chunk Chunk, sealed []byte) ([]byte, error) {
	if generationID == "" || chunk.Index < 0 || chunk.PlaintextSize < 0 || chunk.PlaintextSize > DefaultChunkSize ||
		chunk.CiphertextSize != len(sealed) {
		return nil, errors.New("backup chunk descriptor is invalid")
	}
	checksum := sha256.Sum256(sealed)
	if hex.EncodeToString(checksum[:]) != chunk.CiphertextSHA {
		return nil, errors.New("backup chunk ciphertext checksum differs")
	}
	plaintext, err := cipher.box.Open(
		sealed,
		chunkAdditionalData(cipher.resourceID, generationID, chunk.Index, chunk.PlaintextSize),
	)
	if err != nil {
		return nil, err
	}
	if len(plaintext) != chunk.PlaintextSize {
		clear(plaintext)
		return nil, errors.New("backup chunk plaintext size differs")
	}
	return plaintext, nil
}

type WorkWriter struct {
	cipher       *ResourceCipher
	generationID string
	root         string
	random       io.Reader
	buffer       []byte
	chunks       []WorkChunk
	closed       bool
}

func NewWorkWriter(cipher *ResourceCipher, generationID, root string, random io.Reader) (*WorkWriter, error) {
	if cipher == nil || generationID == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		root == string(filepath.Separator) {
		return nil, errors.New("backup work writer configuration is invalid")
	}
	if random == nil {
		random = rand.Reader
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &WorkWriter{
		cipher: cipher, generationID: generationID, root: root, random: random,
		buffer: make([]byte, 0, DefaultChunkSize),
	}, nil
}

func (writer *WorkWriter) Write(input []byte) (int, error) {
	if writer.closed {
		return 0, errors.New("backup work writer is closed")
	}
	written := 0
	for len(input) != 0 {
		remaining := DefaultChunkSize - len(writer.buffer)
		count := min(remaining, len(input))
		writer.buffer = append(writer.buffer, input[:count]...)
		input = input[count:]
		written += count
		if len(writer.buffer) == DefaultChunkSize {
			if err := writer.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (writer *WorkWriter) Close() error {
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.buffer) != 0 || len(writer.chunks) == 0 {
		return writer.flush()
	}
	return nil
}

func (writer *WorkWriter) Chunks() ([]WorkChunk, error) {
	if !writer.closed {
		return nil, errors.New("backup work writer must be closed before reading chunks")
	}
	return append([]WorkChunk(nil), writer.chunks...), nil
}

func (writer *WorkWriter) flush() error {
	index := len(writer.chunks)
	sealed, descriptor, err := writer.cipher.SealChunk(writer.generationID, index, writer.buffer, writer.random)
	if err != nil {
		return err
	}
	path := filepath.Join(writer.root, fmt.Sprintf("chunk-%08d.pdx", index))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(sealed)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	writer.chunks = append(writer.chunks, WorkChunk{Chunk: descriptor, Path: path})
	clear(writer.buffer)
	writer.buffer = writer.buffer[:0]
	return nil
}

func chunkAdditionalData(resourceID, generationID string, index, plaintextSize int) []byte {
	result := make([]byte, 0, 1+len(resourceID)+len(generationID)+24)
	result = append(result, 1)
	result = appendLengthPrefixed(result, resourceID)
	result = appendLengthPrefixed(result, generationID)
	result = binary.BigEndian.AppendUint64(result, uint64(index))
	result = binary.BigEndian.AppendUint64(result, uint64(plaintextSize))
	return result
}

func appendLengthPrefixed(destination []byte, value string) []byte {
	destination = binary.BigEndian.AppendUint32(destination, uint32(len(value)))
	return append(destination, value...)
}
