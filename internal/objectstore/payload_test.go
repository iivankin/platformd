package objectstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
)

func TestPayloadStoreEncryptsChunksAndReadsBoundedRange(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "objects")
	random := bytes.NewReader(bytes.Repeat([]byte{7}, 3*24))
	store, err := NewPayloadStore(root, cryptobox.MasterKey{1, 2, 3}, random)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := bytes.Repeat([]byte("platformd-secret-value\n"), ChunkSize/20+17)
	info, err := store.Write(context.Background(), "store-id", "payload-id", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	if info.ChunkCount < 2 || info.PlaintextSize != int64(len(plaintext)) {
		t.Fatalf("payload info = %+v", info)
	}
	firstChunk, err := os.ReadFile(filepath.Join(root, "store-id", "payloads", "payload-id", "0.chunk"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(firstChunk, []byte("platformd-secret-value")) {
		t.Fatal("plaintext is visible in encrypted chunk")
	}
	offset := int64(ChunkSize - 13)
	length := int64(60)
	var output bytes.Buffer
	if err := store.ReadRange(context.Background(), "store-id", info, offset, length, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), plaintext[offset:offset+length]) {
		t.Fatalf("range mismatch: %q", output.Bytes())
	}
	firstChunk[len(firstChunk)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, "store-id", "payloads", "payload-id", "0.chunk"), firstChunk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ReadRange(context.Background(), "store-id", info, 0, 1, &bytes.Buffer{}); err == nil {
		t.Fatal("corrupted object chunk was accepted")
	}
}

func TestCredentialSecretRoundTrip(t *testing.T) {
	t.Parallel()
	credentialID := "018bcfe5-687b-7fff-bfff-ffffffffffff"
	accessKey, err := AccessKeyID(credentialID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := CredentialID(accessKey)
	if err != nil || parsed != credentialID {
		t.Fatalf("access key round trip = %q, %v", parsed, err)
	}
	secret, err := GenerateSecret(bytes.NewReader(bytes.Repeat([]byte{4}, secretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{9, 8, 7}
	encrypted, err := SealSecret(master, "store", credentialID, secret)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSecret(master, "store", credentialID, encrypted)
	if err != nil || opened != secret {
		t.Fatalf("secret round trip = %q, %v", opened, err)
	}
}
