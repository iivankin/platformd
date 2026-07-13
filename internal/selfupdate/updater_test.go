package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/state"
)

type releaseFixture struct {
	release bootstrap.VerifiedRelease
	binary  []byte
}

type allowGrowth struct{}

func (allowGrowth) PermitGrowth(context.Context) error { return nil }

type closeFunc func() error

func (close closeFunc) Close() error { return close() }

func TestUpdaterStagesStopsSwitchesAndKeepsAdmissionClosed(t *testing.T) {
	t.Parallel()
	paths, publicKey, privateKey := installedRelease(t, "1.0.0")
	var target releaseFixture
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest.json":
			_, _ = response.Write(target.release.ManifestBytes)
		case "/platformd":
			response.Header().Set("Content-Type", "application/octet-stream")
			_, _ = response.Write(target.binary)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	target = buildRelease(t, filepath.Dir(paths.DataRoot), "2.0.0", server.URL+"/platformd", []string{"1.0.0"}, publicKey, privateKey)

	gate := admission.New()
	stops := 0
	updater, err := New(Config{
		Paths: paths, ExpectedUID: os.Getuid(), ManifestURL: server.URL + "/latest.json",
		PublicKey: publicKey, HTTPClient: server.Client(), Admission: gate,
		Growth:           allowGrowth{},
		QuiesceWorkloads: func(context.Context) (ResumeWorkloads, error) { stops++; return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := updater.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousVersion != "1.0.0" || result.TargetVersion != "2.0.0" || stops != 1 {
		t.Fatalf("update result = %+v, stops=%d", result, stops)
	}
	assertLink(t, paths.Current, "2.0.0")
	assertLink(t, paths.Previous, "1.0.0")
	if _, err := bootstrap.CurrentReleaseManifest(paths, publicKey, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Begin("deploy", "after-switch"); !errors.Is(err, admission.ErrUpdating) {
		t.Fatalf("post-switch admission = %v", err)
	}
	if err := bootstrap.FinalizeSuccessfulUpdate(paths, publicKey, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.Previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous link survived readiness: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(paths.ReleasesRoot, "1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous slot survived readiness: %v", err)
	}
}

func TestUpdaterReportsBoundedBlockersBeforePublishingSlot(t *testing.T) {
	t.Parallel()
	paths, publicKey, privateKey := installedRelease(t, "1.0.0")
	target, server := servedRelease(t, filepath.Dir(paths.DataRoot), "2.0.0", []string{"1.0.0"}, publicKey, privateKey)
	gate := admission.New()
	blocker, err := gate.Begin("backup", "backup-id")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	updater, err := New(Config{
		Paths: paths, ExpectedUID: os.Getuid(), ManifestURL: server.URL + "/latest.json",
		PublicKey: publicKey, HTTPClient: server.Client(), Admission: gate,
		Growth: allowGrowth{},
		QuiesceWorkloads: func(context.Context) (ResumeWorkloads, error) {
			t.Fatal("workloads stopped while busy")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = updater.Apply(context.Background())
	var busy BusyError
	if !errors.As(err, &busy) || busy.Snapshot.Total != 1 || busy.Snapshot.Blockers[0].ID != "backup-id" {
		t.Fatalf("busy update = %+v, %v", busy, err)
	}
	if _, err := os.Lstat(filepath.Join(paths.ReleasesRoot, target.release.Manifest.Version)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("busy target slot exists: %v", err)
	}
	assertLink(t, paths.Current, "1.0.0")
}

func TestUpdaterLeavesVerifiedInactiveSlotAndReopensGateWhenStopFails(t *testing.T) {
	t.Parallel()
	paths, publicKey, privateKey := installedRelease(t, "1.0.0")
	target, server := servedRelease(t, filepath.Dir(paths.DataRoot), "2.0.0", []string{"1.0.0"}, publicKey, privateKey)
	gate := admission.New()
	resumed := false
	updater, err := New(Config{
		Paths: paths, ExpectedUID: os.Getuid(), ManifestURL: server.URL + "/latest.json",
		PublicKey: publicKey, HTTPClient: server.Client(), Admission: gate,
		Growth: allowGrowth{},
		QuiesceWorkloads: func(context.Context) (ResumeWorkloads, error) {
			return func(context.Context) error {
				lease, err := gate.Begin("service_reconcile", "service")
				if err != nil {
					return err
				}
				lease.Release()
				resumed = true
				return nil
			}, errors.New("stop timeout")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "stop timeout") {
		t.Fatalf("stop failure = %v", err)
	}
	if !resumed {
		t.Fatal("partial quiesce was not resumed after gate release")
	}
	assertLink(t, paths.Current, "1.0.0")
	if _, err := os.Lstat(paths.Previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous link after aborted switch = %v", err)
	}
	if err := bootstrap.VerifyReleaseSlot(filepath.Join(paths.ReleasesRoot, target.release.Manifest.Version), target.release.ManifestBytes, publicKey, os.Getuid()); err != nil {
		t.Fatalf("inactive target slot = %v", err)
	}
	lease, err := gate.Begin("deploy", "retry")
	if err != nil {
		t.Fatalf("gate remained closed: %v", err)
	}
	lease.Release()
}

func TestPreviousBinaryRollbackIsAllowedOnlyAtItsSchemaVersion(t *testing.T) {
	t.Parallel()
	paths, publicKey, privateKey := installedRelease(t, "1.0.0")
	_, server := servedRelease(t, filepath.Dir(paths.DataRoot), "2.0.0", []string{"1.0.0"}, publicKey, privateKey)
	gate := admission.New()
	updater, err := New(Config{
		Paths: paths, ExpectedUID: os.Getuid(), ManifestURL: server.URL + "/latest.json",
		PublicKey: publicKey, HTTPClient: server.Client(), Admission: gate, Growth: allowGrowth{},
		QuiesceWorkloads: func(context.Context) (ResumeWorkloads, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(context.Background(), paths.StateDatabase, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	starts := 0
	rollback := bootstrap.UpdateRollback{
		Paths: paths, ExpectedUID: os.Getuid(), PublicKey: publicKey,
		SchemaVersion: state.SupportedSchemaVersion(),
		Executable:    filepath.Join(paths.Previous, "platformd"),
		AcquireLock: func(string, int) (io.Closer, error) {
			return closeFunc(func() error { return nil }), nil
		},
		StartService: func(context.Context) error { starts++; return nil },
	}
	wrongSchema := rollback
	wrongSchema.SchemaVersion++
	if err := wrongSchema.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "rollback is forbidden") {
		t.Fatalf("post-migration rollback = %v", err)
	}
	assertLink(t, paths.Current, "2.0.0")
	if starts != 0 {
		t.Fatalf("wrong-schema rollback started service %d times", starts)
	}
	if err := rollback.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLink(t, paths.Current, "1.0.0")
	if starts != 1 {
		t.Fatalf("service starts = %d", starts)
	}
	if _, err := os.Lstat(paths.Previous); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous link survived rollback: %v", err)
	}
}

func TestSavedPreviousBinaryInstallsSignedForwardFixWithoutOpeningState(t *testing.T) {
	t.Parallel()
	paths, publicKey, privateKey := installedRelease(t, "1.0.0")
	_, server := servedRelease(t, filepath.Dir(paths.DataRoot), "2.0.0", []string{"1.0.0"}, publicKey, privateKey)
	updater, err := New(Config{
		Paths: paths, ExpectedUID: os.Getuid(), ManifestURL: server.URL + "/latest.json",
		PublicKey: publicKey, HTTPClient: server.Client(), Admission: admission.New(), Growth: allowGrowth{},
		QuiesceWorkloads: func(context.Context) (ResumeWorkloads, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	forward := buildRelease(t, filepath.Dir(paths.DataRoot), "3.0.0", "https://example.com/platformd", []string{"2.0.0"}, publicKey, privateKey)
	manifestPath := filepath.Join(t.TempDir(), "forward.json")
	if err := os.WriteFile(manifestPath, forward.release.ManifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	starts := 0
	installer := bootstrap.SignedUpdateInstaller{
		Paths: paths, ExpectedUID: os.Getuid(), PublicKey: publicKey,
		Executable: filepath.Join(paths.Previous, "platformd"), ManifestSource: manifestPath,
		BinaryPath: forward.release.ExecutablePath,
		AcquireLock: func(string, int) (io.Closer, error) {
			return closeFunc(func() error { return nil }), nil
		},
		StartService: func(context.Context) error { starts++; return nil },
	}
	if err := installer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLink(t, paths.Current, "3.0.0")
	assertLink(t, paths.Previous, "2.0.0")
	if starts != 1 {
		t.Fatalf("forward fix service starts = %d", starts)
	}
	if _, err := os.Lstat(paths.StateDatabase); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forward installer opened or created SQLite: %v", err)
	}
}

func installedRelease(t *testing.T, version string) (layout.Paths, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	root := t.TempDir()
	paths := layout.FromRoots(filepath.Join(root, "data"), filepath.Join(root, "config"), filepath.Join(root, "run"), filepath.Join(root, "bin", "platformd"), filepath.Join(root, "platformd.service"))
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	release := buildRelease(t, root, version, "https://example.com/platformd", nil, publicKey, privateKey)
	if err := bootstrap.PublishReleaseSlot(release.release, paths, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.SwitchCurrentRelease(paths, version); err != nil {
		t.Fatal(err)
	}
	return paths, publicKey, privateKey
}

func servedRelease(t *testing.T, root, version string, supported []string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) (releaseFixture, *httptest.Server) {
	t.Helper()
	var fixture releaseFixture
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest.json" {
			_, _ = response.Write(fixture.release.ManifestBytes)
			return
		}
		if request.URL.Path == "/platformd" {
			_, _ = response.Write(fixture.binary)
			return
		}
		http.NotFound(response, request)
	}))
	t.Cleanup(server.Close)
	fixture = buildRelease(t, root, version, server.URL+"/platformd", supported, publicKey, privateKey)
	return fixture, server
}

func buildRelease(t *testing.T, root, version, binaryURL string, supported []string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) releaseFixture {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "platformd")
	if err := os.WriteFile(executable, []byte("\x7fELF-platformd-"+version), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(directory, "bundle")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeUpdateRuntimeProfile(t, runtimeRoot, version)
	if err := releasebundle.Append(executable, runtimeRoot); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	manifestBytes, err := releasemanifest.Sign(releasemanifest.Manifest{
		Architecture: "amd64", BinarySHA256: hex.EncodeToString(hash[:]), BinarySize: int64(len(binary)),
		BinaryURL: binaryURL, Format: 1, OS: "linux", SupportedFrom: supported, Version: version,
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	_ = root
	return releaseFixture{
		release: bootstrap.VerifiedRelease{ExecutablePath: executable, Manifest: manifest, ManifestBytes: manifestBytes, PublicKey: publicKey},
		binary:  binary,
	}
}

func writeUpdateRuntimeProfile(t *testing.T, root, version string) {
	t.Helper()
	for _, name := range []string{"catatonit", "conmon", "crun", "netavark"} {
		value := []byte("#!/bin/sh\n# " + version + "\nexit 0\n")
		if err := os.WriteFile(filepath.Join(root, name), value, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"containers.conf", "mounts.conf", "policy.json", "registries.conf", "seccomp.json", "storage.conf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertLink(t *testing.T, path, want string) {
	t.Helper()
	value, err := os.Readlink(path)
	if err != nil || value != want {
		t.Fatalf("link %s = %q, %v; want %q", path, value, err, want)
	}
}
