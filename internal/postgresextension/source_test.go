package postgresextension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSourceCacheVerifiesAndRepairsPinnedArchive(t *testing.T) {
	payload := []byte("pgvector source archive")
	hash := sha256.Sum256(payload)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	recipe := Recipe{
		Name: "vector", Version: "test", SourceURL: server.URL,
		SourceSHA256: hex.EncodeToString(hash[:]),
	}
	cache := sourceCache{root: t.TempDir(), client: server.Client()}
	path, err := cache.ensure(context.Background(), recipe)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests = %d, want 1", requests.Load())
	}
	if _, err := cache.ensure(context.Background(), recipe); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("verified source was downloaded again: requests=%d", requests.Load())
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.ensure(context.Background(), recipe); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("corrupt source was not repaired: requests=%d", requests.Load())
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != string(payload) {
		t.Fatalf("repaired source = %q, %v", stored, err)
	}
}

func TestSourceCacheRejectsChecksumMismatchWithoutPublishingArchive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("untrusted payload"))
	}))
	defer server.Close()
	root := t.TempDir()
	cache := sourceCache{root: root, client: server.Client()}
	recipe := Recipe{
		Name: "vector", Version: "test", SourceURL: server.URL,
		SourceSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if _, err := cache.ensure(context.Background(), recipe); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "sources", "vector-test.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("unverified archive was published: %v", err)
	}
}
