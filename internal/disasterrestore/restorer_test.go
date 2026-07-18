package disasterrestore_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/backup"
	"github.com/iivankin/platformd/internal/bootstrap"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/disasterrestore"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/masterkey"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releasemanifest"
	"github.com/iivankin/platformd/internal/remotes3"
	"github.com/iivankin/platformd/internal/state"
)

type restoreRemote struct {
	objects map[string][]byte
	probed  bool
}

func newRestoreRemote() *restoreRemote { return &restoreRemote{objects: make(map[string][]byte)} }

func (*restoreRemote) Key(relative string) string {
	return "prefix/" + strings.TrimPrefix(relative, "/")
}

func (remote *restoreRemote) Probe(context.Context) error { remote.probed = true; return nil }

func (remote *restoreRemote) Put(_ context.Context, key string, input io.Reader, size int64, checksum string) error {
	value, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(value)
	if int64(len(value)) != size || hex.EncodeToString(hash[:]) != checksum {
		return io.ErrUnexpectedEOF
	}
	remote.objects[key] = append([]byte(nil), value...)
	return nil
}

func (remote *restoreRemote) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	value, exists := remote.objects[key]
	if !exists {
		return nil, 0, &remotes3.RemoteError{StatusCode: 404, Code: "NoSuchKey"}
	}
	return io.NopCloser(bytes.NewReader(value)), int64(len(value)), nil
}

func (remote *restoreRemote) Delete(_ context.Context, key string) error {
	delete(remote.objects, key)
	return nil
}

func (remote *restoreRemote) List(_ context.Context, prefix, _ string) (remotes3.Page, error) {
	keys := make([]string, 0)
	for key := range remote.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	page := remotes3.Page{Objects: make([]remotes3.Object, 0, len(keys))}
	for _, key := range keys {
		page.Objects = append(page.Objects, remotes3.Object{Key: key, Size: int64(len(remote.objects[key]))})
	}
	return page, nil
}

type restoreServices struct {
	reloads int
	starts  int
	healths int
	host    string
}

func (services *restoreServices) ReloadAndEnable(context.Context) error {
	services.reloads++
	return nil
}
func (services *restoreServices) Start(context.Context) error { services.starts++; return nil }
func (services *restoreServices) Health(_ context.Context, host, certificate string) error {
	services.healths++
	services.host = host
	if certificate == "" {
		return errors.New("empty health certificate")
	}
	return nil
}

func TestRestorerPublishesVerifiedControlStateAndStartsExactRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceRoot := t.TempDir()
	sourcePaths, publicKey := restoreReleaseSlot(t, sourceRoot)
	sourceStore, err := state.Open(ctx, filepath.Join(sourceRoot, "state", "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	certificate := restoreCertificate(t, "admin.example.com")
	if err := sourceStore.CreateInstallation(ctx, state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com", AccessTeamDomain: "team.cloudflareaccess.com",
		AccessAudience: "audience", ConsolePassphrasePHC: "verifier", OriginCertificateID: "certificate",
		OriginCertificatePEM: certificate, OriginPrivateKey: []byte("encrypted"),
		InitialAuditEventID: "initial-audit", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3, 4}
	sealedTargetSecret, err := backup.SealTargetSecret(master, "installation", "old-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.SetBackupTarget(ctx, state.SetBackupTarget{
		Target: state.BackupTarget{
			ID: "recovery-target", Name: "Offsite", Endpoint: "https://s3.example.com", Region: "region",
			Bucket: "bucket", Prefix: "prefix", AccessKeyID: "old-access",
			SecretAccessKeyEncrypted: sealedTargetSecret,
		},
		AuditEventID: "target-audit", ActorKind: "access", ActorID: "user",
		ActorEmail: "admin@example.com", UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.SetControlBackupTarget(ctx, state.SetControlBackupTarget{
		TargetID: "recovery-target", AuditEventID: "control-target-audit", ActorKind: "access",
		ActorID: "user", ActorEmail: "admin@example.com", UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	built, err := backup.BuildControl(ctx, backup.ControlBuildConfig{
		Store: sourceStore, Master: master, InstallationID: "installation", GenerationID: "generation",
		ReleaseSlot: filepath.Join(sourcePaths.ReleasesRoot, "1.2.3"), WorkRoot: filepath.Join(sourceRoot, "work"),
		ExpectedUID: os.Geteuid(), PublicKey: publicKey, CreatedAt: time.Unix(10, 0), Random: rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(built.WorkDirectory)
	remote := newRestoreRemote()
	if err := backup.PublishControl(ctx, remote, master, built); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	destinationRoot := t.TempDir()
	paths := layout.FromRoots(
		filepath.Join(destinationRoot, "data"), filepath.Join(destinationRoot, "config"), filepath.Join(destinationRoot, "run"),
		filepath.Join(destinationRoot, "bin", "platformd"), filepath.Join(destinationRoot, "systemd", "platformd.service"),
	)
	services := &restoreServices{}
	validated, err := disasterrestore.ValidateInput(disasterrestore.Input{
		MasterRecoveryKey: masterkey.RecoveryString(master), Endpoint: "https://s3.example.com", Region: "region",
		Bucket: "bucket", Prefix: "prefix", AccessKeyID: "access", SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	makeRestorer := func(paths layout.Paths, architecture string, services *restoreServices) disasterrestore.Restorer {
		return disasterrestore.Restorer{
			Paths: paths, ExpectedUID: os.Geteuid(), PublicKey: publicKey,
			ProvideInput:  func() (disasterrestore.ValidatedInput, error) { return validated, nil },
			ValidateHost:  func(context.Context, string) error { return nil },
			RemoteFactory: func(remotes3.Config) (disasterrestore.RestoreRemote, error) { return remote, nil },
			ImportExact: func(ctx context.Context, _ string, payload disasterrestore.ImportPayload) (disasterrestore.ImportResult, error) {
				return disasterrestore.ImportSnapshot(ctx, payload)
			},
			AcquireLock: func(string, int) (io.Closer, error) { return io.NopCloser(strings.NewReader("")), nil },
			Services:    services, Now: func() time.Time { return time.Unix(20, 0) }, Random: rand.Reader,
			OS: "linux", Architecture: architecture,
		}
	}

	mismatchRoot := t.TempDir()
	mismatchPaths := layout.FromRoots(
		filepath.Join(mismatchRoot, "data"), filepath.Join(mismatchRoot, "config"), filepath.Join(mismatchRoot, "run"),
		filepath.Join(mismatchRoot, "bin", "platformd"), filepath.Join(mismatchRoot, "systemd", "platformd.service"),
	)
	mismatchServices := &restoreServices{}
	if err := makeRestorer(mismatchPaths, "arm64", mismatchServices).Restore(ctx); err == nil || !strings.Contains(err.Error(), "cannot restore") {
		t.Fatalf("cross-architecture restore error = %v", err)
	}
	for _, path := range []string{mismatchPaths.DataRoot, mismatchPaths.ConfigRoot} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cross-architecture restore created persistent path %s: %v", path, err)
		}
	}
	if entries, err := os.ReadDir(mismatchRoot); err != nil || len(entries) != 0 {
		t.Fatalf("cross-architecture restore left preflight state: entries=%v err=%v", entries, err)
	}

	restorer := makeRestorer(paths, "amd64", services)
	if err := restorer.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if !remote.probed || services.reloads != 1 || services.starts != 1 || services.healths != 1 || services.host != "admin.example.com" {
		t.Fatalf("restore effects: probed=%v services=%+v", remote.probed, services)
	}
	installedMaster, err := masterkey.Load(paths.MasterKey, os.Geteuid())
	if err != nil || installedMaster != master {
		t.Fatalf("installed master key = %v, %v", installedMaster, err)
	}
	current, err := os.Readlink(paths.Current)
	if err != nil || current != "1.2.3" {
		t.Fatalf("current release = %q, %v", current, err)
	}
	store, err := state.Open(ctx, paths.StateDatabase, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	installation, err := store.Installation(ctx)
	if err != nil || !installation.RecoveryMode || installation.AdminHostname != "admin.example.com" {
		t.Fatalf("restored installation = %+v, %v", installation, err)
	}
	target, err := store.BackupTarget(ctx, "recovery-target")
	if err != nil || target.AccessKeyID != "access" {
		t.Fatalf("restored target = %+v, %v", target, err)
	}
}

func restoreReleaseSlot(t *testing.T, root string) (layout.Paths, ed25519.PublicKey) {
	t.Helper()
	build := t.TempDir()
	executable := filepath.Join(build, "platformd")
	if err := os.WriteFile(executable, []byte("\x7fELF-restored-platformd"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(build, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRestoreRuntimeProfile(t, runtimeRoot)
	if err := releasebundle.Append(executable, runtimeRoot); err != nil {
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
		Architecture: "amd64", BinarySHA256: hex.EncodeToString(hash[:]), BinarySize: int64(len(binary)),
		BinaryURL: "https://example.com/platformd", Format: releasemanifest.FormatVersion, OS: "linux", Version: "1.2.3",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := releasemanifest.ParseAndVerify(manifestBytes, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	paths := layout.FromRoots(
		filepath.Join(root, "release-data"), filepath.Join(root, "release-config"), filepath.Join(root, "release-run"),
		filepath.Join(root, "release-bin", "platformd"), filepath.Join(root, "release-systemd", "platformd.service"),
	)
	if err := bootstrap.PublishReleaseSlot(bootstrap.VerifiedRelease{
		ExecutablePath: executable, Manifest: manifest, ManifestBytes: manifestBytes, PublicKey: publicKey,
	}, paths, os.Geteuid()); err != nil {
		t.Fatal(err)
	}
	return paths, publicKey
}

func writeRestoreRuntimeProfile(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"catatonit", "conmon", "crun", "netavark"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"containers.conf", "mounts.conf", "policy.json", "registries.conf", "seccomp.json", "storage.conf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
