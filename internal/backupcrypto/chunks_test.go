package backupcrypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestEncryptedWorkChunksRoundTripWithoutPlaintextFiles(t *testing.T) {
	t.Parallel()
	cipher, err := NewResourceCipher(cryptobox.MasterKey{1, 2, 3}, "registry")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "work")
	random := bytes.NewReader(bytes.Repeat([]byte{0x42}, 24*5))
	writer, err := NewWorkWriter(cipher, "generation", root, random)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := bytes.Repeat([]byte("sensitive-registry-manifest"), DefaultChunkSize/8)
	if _, err := writer.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	chunks, err := writer.Chunks()
	if err != nil || len(chunks) < 2 {
		t.Fatalf("chunks = %d, %v", len(chunks), err)
	}
	var restored []byte
	for _, chunk := range chunks {
		sealed, err := os.ReadFile(chunk.Path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, []byte("sensitive-registry-manifest")) {
			t.Fatal("plaintext appeared in encrypted work file")
		}
		opened, err := cipher.OpenChunk("generation", chunk.Chunk, sealed)
		if err != nil {
			t.Fatal(err)
		}
		restored = append(restored, opened...)
		clear(opened)
	}
	if !bytes.Equal(restored, plaintext) {
		t.Fatal("restored backup chunks differ")
	}
}

func TestChunkIdentityAndCiphertextAreAuthenticated(t *testing.T) {
	t.Parallel()
	cipher, err := NewResourceCipher(cryptobox.MasterKey{4, 5, 6}, "resource")
	if err != nil {
		t.Fatal(err)
	}
	sealed, chunk, err := cipher.SealChunk(
		"generation", 0, []byte("payload"), bytes.NewReader(bytes.Repeat([]byte{0x24}, 24)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, err := cipher.OpenChunk("generation", chunk, tampered); err == nil {
		t.Fatal("tampered backup chunk was accepted")
	}
	checksum := sha256.Sum256(sealed)
	chunk.CiphertextSHA = hex.EncodeToString(checksum[:])
	if _, err := cipher.OpenChunk("other-generation", chunk, sealed); err == nil {
		t.Fatal("backup chunk was accepted for another generation")
	}
}
