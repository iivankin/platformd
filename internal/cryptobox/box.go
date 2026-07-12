package cryptobox

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	keySize    = 32
	formatSize = 4
)

var format = [formatSize]byte{'P', 'D', 'X', 1}

type MasterKey [keySize]byte

type Box struct {
	key [keySize]byte
}

func ParseMasterKey(value []byte) (MasterKey, error) {
	if len(value) != keySize {
		return MasterKey{}, fmt.Errorf("master key length = %d, want %d", len(value), keySize)
	}
	var key MasterKey
	copy(key[:], value)
	return key, nil
}

func NewBox(master MasterKey, salt []byte, info string) (Box, error) {
	if len(salt) == 0 {
		return Box{}, errors.New("empty key-derivation salt")
	}
	if info == "" {
		return Box{}, errors.New("empty key-derivation domain")
	}
	derived, err := hkdf.Key(sha256.New, master[:], salt, info, keySize)
	if err != nil {
		return Box{}, fmt.Errorf("derive key: %w", err)
	}
	var key [keySize]byte
	copy(key[:], derived)
	clear(derived)
	return Box{key: key}, nil
}

func (box Box) Seal(plaintext, additionalData []byte) ([]byte, error) {
	return box.SealWith(rand.Reader, plaintext, additionalData)
}

func (box Box) SealWith(random io.Reader, plaintext, additionalData []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(box.key[:])
	if err != nil {
		return nil, fmt.Errorf("create XChaCha20-Poly1305: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	sealed := make([]byte, 0, formatSize+len(nonce)+len(plaintext)+aead.Overhead())
	sealed = append(sealed, format[:]...)
	sealed = append(sealed, nonce...)
	sealed = aead.Seal(sealed, nonce, plaintext, additionalData)
	return sealed, nil
}

func (box Box) Open(sealed, additionalData []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(box.key[:])
	if err != nil {
		return nil, fmt.Errorf("create XChaCha20-Poly1305: %w", err)
	}
	headerSize := formatSize + aead.NonceSize()
	if len(sealed) < headerSize+aead.Overhead() {
		return nil, errors.New("encrypted value is truncated")
	}
	if string(sealed[:formatSize]) != string(format[:]) {
		return nil, errors.New("unsupported encrypted value format")
	}
	nonce := sealed[formatSize:headerSize]
	plaintext, err := aead.Open(nil, nonce, sealed[headerSize:], additionalData)
	if err != nil {
		return nil, errors.New("encrypted value authentication failed")
	}
	return plaintext, nil
}
