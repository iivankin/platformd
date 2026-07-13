//go:build linux && amd64 && cgo && integration

package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/state"
)

func TestMain(m *testing.M) {
	if containerengine.InitReexec() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEmbeddedRegistryLocalPullImportsIntoContainersStorage(t *testing.T) {
	if os.Getenv("PLATFORMD_REGISTRY_LOCAL_PULL_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_REGISTRY_LOCAL_PULL_INTEGRATION=1 on an isolated root Linux host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := "/var/lib/platformd-registry-local-pull-integration"
	runtimeRoot := "/run/platformd-registry-local-pull-integration"
	_ = os.RemoveAll(root)
	_ = os.RemoveAll(runtimeRoot)
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
		_ = os.RemoveAll(runtimeRoot)
	})

	store, err := state.Open(ctx, filepath.Join(root, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payloads, err := NewPayloadStore(filepath.Join(root, "registry"))
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, cryptobox.MasterKey{1, 2, 3}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.CreateRepository(ctx, CreateRepositoryInput{
		Name: "integration/local", Actor: Actor{Kind: "access", ID: "test", Email: "test@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := application.Authenticate(ctx, created.Repository.Name, created.Username, created.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	layer, diffID := emptyImageLayer(t)
	layerDigest := uploadTestBlob(t, ctx, application, authentication, layer)
	configBody, err := json.Marshal(map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config":       map[string]any{},
		"rootfs": map[string]any{
			"type": "layers", "diff_ids": []string{diffID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configDigest := uploadTestBlob(t, ctx, application, authentication, configBody)
	manifestBody := []byte(fmt.Sprintf(`{
  "schemaVersion":2,
  "mediaType":%q,
  "config":{"mediaType":"application/vnd.oci.image.config.v1+json","size":%d,"digest":%q},
  "layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","size":%d,"digest":%q}]
}`, OCIImageManifestMediaType, len(configBody), configDigest, len(layer), layerDigest))
	manifest, err := application.PutManifest(ctx, authentication, "latest", OCIImageManifestMediaType, manifestBody)
	if err != nil {
		t.Fatal(err)
	}
	local, err := application.PrepareLocalPull(ctx, filepath.Join(runtimeRoot, "generated"), created.Repository.Name, "latest")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	paths := layout.FromRoots(root, filepath.Join(root, "config"), runtimeRoot, filepath.Join(root, "platformd"), filepath.Join(root, "platformd.service"))
	config := containerengine.ProductionConfig(paths, "/workloads")
	assetRoot := os.Getenv("PLATFORMD_RUNTIME_ASSET_ROOT")
	if assetRoot == "" {
		assetRoot = "/var/lib/platformd/releases/current/runtime"
	}
	config.ContainersConf = filepath.Join(assetRoot, "containers.conf")
	config.StorageConf = filepath.Join(assetRoot, "storage.conf")
	config.RegistriesConf = filepath.Join(assetRoot, "registries.conf")
	config.SignaturePolicy = filepath.Join(assetRoot, "policy.json")
	config.SeccompProfile = filepath.Join(assetRoot, "seccomp.json")
	config.DefaultMountsFile = filepath.Join(assetRoot, "mounts.conf")
	config.OCIRuntime = filepath.Join(assetRoot, "crun")
	config.Conmon = filepath.Join(assetRoot, "conmon")
	if _, err := containerengine.PrepareStorage(ctx, config); err != nil {
		t.Fatal(err)
	}
	engine, err := containerengine.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	image, err := engine.Pull(ctx, containerengine.PullRequest{Reference: local.Reference})
	if err != nil {
		t.Fatal(err)
	}
	if image.ID == "" || image.Digest != manifest.Digest {
		t.Fatalf("imported image = %+v, want digest %s", image, manifest.Digest)
	}
}

func emptyImageLayer(t *testing.T) ([]byte, string) {
	t.Helper()
	var uncompressed bytes.Buffer
	writer := tar.NewWriter(&uncompressed)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	diff := sha256.Sum256(uncompressed.Bytes())
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(uncompressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes(), fmt.Sprintf("sha256:%x", diff)
}
