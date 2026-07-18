package volume

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestLiveBackupRestoresOrdinaryVolumeThroughAtomicReplacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stored := state.Volume{
		ID: "volume", ProjectID: "project", ServiceID: "service", Name: "data",
		OwnerUID: os.Geteuid(), OwnerGID: os.Getegid(),
	}
	live := filepath.Join(root, stored.ProjectID, stored.ID)
	if err := os.MkdirAll(filepath.Join(live, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "nested", "data.txt"), []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested/data.txt", filepath.Join(live, "current")); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenLiveBackup(context.Background(), root, stored)
	if err != nil {
		t.Fatal(err)
	}
	archive, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "nested", "data.txt"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "extra.txt"), []byte("remove me"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RestoreBackup(context.Background(), root, stored, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(live, "nested", "data.txt"))
	if err != nil || string(value) != "before" {
		t.Fatalf("restored file = %q, %v", value, err)
	}
	if _, err := os.Lstat(filepath.Join(live, "extra.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-backup file survived replacement: %v", err)
	}
	link, err := os.Readlink(filepath.Join(live, "current"))
	if err != nil || link != "nested/data.txt" {
		t.Fatalf("restored symlink = %q, %v", link, err)
	}
	if _, err := os.Lstat(live + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous volume remains after committed restore: %v", err)
	}
}

func TestVolumeRestoreRejectsTraversalBeforeReplacingLiveData(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stored := state.Volume{
		ID: "volume", ProjectID: "project", ServiceID: "service", Name: "data",
		OwnerUID: os.Geteuid(), OwnerGID: os.Getegid(),
	}
	live := filepath.Join(root, stored.ProjectID, stored.ID)
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "keep.txt"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{
		Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1,
		Uid: os.Geteuid(), Gid: os.Getegid(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := RestoreBackup(context.Background(), root, stored, bytes.NewReader(archive.Bytes())); err == nil {
		t.Fatal("path traversal archive was restored")
	}
	value, err := os.ReadFile(filepath.Join(live, "keep.txt"))
	if err != nil || string(value) != "live" {
		t.Fatalf("failed restore changed live data = %q, %v", value, err)
	}
	if _, err := os.Lstat(filepath.Join(root, stored.ProjectID, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("traversal path was created: %v", err)
	}
}

func TestLiveBackupRejectsEscapingVolumeIdentity(t *testing.T) {
	t.Parallel()
	_, err := OpenLiveBackup(context.Background(), t.TempDir(), state.Volume{
		ID: "volume", ProjectID: "..", ServiceID: "service", Name: "data",
		OwnerUID: os.Geteuid(), OwnerGID: os.Getegid(),
	})
	if err == nil {
		t.Fatal("escaping project identity was accepted")
	}
}
