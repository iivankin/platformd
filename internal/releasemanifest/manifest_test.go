package releasemanifest_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/releasemanifest"
)

func TestSignParseVerifyBinaryAndUpdateContract(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("platformd release bytes")
	hash := sha256.Sum256(binary)
	manifest := releasemanifest.Manifest{
		Architecture:  "amd64",
		BinarySHA256:  hex.EncodeToString(hash[:]),
		BinarySize:    int64(len(binary)),
		BinaryURL:     "https://github.com/iivankin/platformd/releases/download/v1.2.0/platformd-linux-amd64",
		Format:        1,
		OS:            "linux",
		SupportedFrom: []string{"1.0.0", "1.1.0"},
		Version:       "1.2.0",
	}
	encoded, err := releasemanifest.Sign(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := releasemanifest.ParseAndVerify(encoded, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.AllowsUpdateFrom("1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := verified.AllowsUpdateFrom("1.2.0"); err == nil {
		t.Fatal("reinstall was accepted")
	}
	path := filepath.Join(t.TempDir(), "platformd")
	if err := os.WriteFile(path, binary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verified.VerifyBinary(path); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsTamperingDuplicateKeysAndWrongTarget(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("binary"))
	encoded, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture: "amd64",
		BinarySHA256: hex.EncodeToString(hash[:]),
		BinarySize:   6,
		BinaryURL:    "https://example.com/platformd",
		Format:       1,
		OS:           "linux",
		Version:      "1.0.0",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(encoded), `"version":"1.0.0"`, `"version":"2.0.0"`, 1)
	if _, err := releasemanifest.ParseAndVerify([]byte(tampered), publicKey); err == nil {
		t.Fatal("tampered manifest was accepted")
	}
	duplicate := strings.Replace(string(encoded), `{"architecture":`, `{"version":"1.0.0","architecture":`, 1)
	if _, err := releasemanifest.ParseAndVerify([]byte(duplicate), publicKey); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate key error = %v", err)
	}
	wrongTarget := strings.Replace(string(encoded), `"amd64"`, `"arm64"`, 1)
	if _, err := releasemanifest.ParseAndVerify([]byte(wrongTarget), publicKey); err == nil {
		t.Fatal("wrong target manifest was accepted")
	}
}
