package bootstrap_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
)

func TestLoadReleaseVerifiesManifestBinaryAndBundle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executable := filepath.Join(root, "platformd")
	if err := os.WriteFile(executable, []byte("\x7fELFtest-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Join(root, "runtime-source")
	if err := os.Mkdir(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeProfile(t, runtimeDirectory)
	if err := releasebundle.Append(executable, runtimeDirectory); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture: "amd64",
		BinarySHA256: hex.EncodeToString(hash[:]),
		BinarySize:   int64(len(binary)),
		BinaryURL:    "https://example.com/platformd",
		Format:       1,
		OS:           "linux",
		Version:      "1.0.0",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(manifestBytes)
	}))
	defer server.Close()

	release, err := bootstrap.LoadRelease(context.Background(), bootstrap.ReleaseLoaderConfig{
		ExecutablePath: executable,
		Version:        "1.0.0",
		ManifestURL:    server.URL,
		PublicKey:      publicKey,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(root, "extracted")
	if err := release.ExtractRuntime(extracted); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(filepath.Join(extracted, "runtime", "crun")); err != nil || string(value) != "#!/bin/sh\nexit 0\n" {
		t.Fatalf("extracted runtime = %q, %v", value, err)
	}
}
