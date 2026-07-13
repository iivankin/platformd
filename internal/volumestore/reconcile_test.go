package volumestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestReconcileRemovesOrphansAndCreatesMissingOrdinaryVolumes(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "volumes")
	mustMkdir(t, filepath.Join(root, "project", "ordinary-existing"), 0o711)
	mustMkdir(t, filepath.Join(root, "project", "postgres-active"), 0o700)
	mustMkdir(t, filepath.Join(root, "project", "restore-candidate"), 0o700)
	mustMkdir(t, filepath.Join(root, "deleted-project", "old-volume"), 0o700)
	contentPath := filepath.Join(root, "project", "ordinary-existing", "data")
	if err := os.WriteFile(contentPath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Reconcile(context.Background(), root, []state.PersistentVolumeReference{
		{ProjectID: "project", VolumeID: "ordinary-existing", Kind: state.PersistentVolumeOrdinary, OwnerUID: os.Geteuid(), OwnerGID: os.Getegid()},
		{ProjectID: "project", VolumeID: "ordinary-missing", Kind: state.PersistentVolumeOrdinary, OwnerUID: os.Geteuid(), OwnerGID: os.Getegid()},
		{ProjectID: "project", VolumeID: "postgres-active", Kind: state.PersistentVolumePostgres},
		{ProjectID: "project", VolumeID: "redis-missing", Kind: state.PersistentVolumeRedis},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Removed != 2 {
		t.Fatalf("reconcile result = %+v", result)
	}
	if content, err := os.ReadFile(contentPath); err != nil || string(content) != "preserved" {
		t.Fatalf("existing ordinary volume content = %q, %v", content, err)
	}
	if info, err := os.Stat(filepath.Join(root, "project", "ordinary-existing")); err != nil || info.Mode().Perm() != 0o711 {
		t.Fatalf("existing ordinary volume mode = %v, %v", info, err)
	}
	missing := filepath.Join(root, "project", "ordinary-missing")
	if info, err := os.Stat(missing); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("created ordinary volume = %v, %v", info, err)
	}
	if info, err := os.Stat(missing); err != nil {
		t.Fatal(err)
	} else if uid, gid := fileOwner(info); uid != os.Geteuid() || gid != os.Getegid() {
		t.Fatalf("created ordinary volume owner = %d:%d", uid, gid)
	}
	for _, removed := range []string{
		filepath.Join(root, "project", "restore-candidate"),
		filepath.Join(root, "deleted-project"),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("orphan %s remains: %v", removed, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "project", "redis-missing")); !os.IsNotExist(err) {
		t.Fatalf("missing managed volume was initialized by cleanup: %v", err)
	}
}

func TestReconcileRejectsReferencedSymlinkWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "volumes")
	project := filepath.Join(root, "project")
	mustMkdir(t, project, 0o700)
	target := filepath.Join(t.TempDir(), "target")
	mustMkdir(t, target, 0o700)
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(project, "volume")); err != nil {
		t.Fatal(err)
	}

	_, err := Reconcile(context.Background(), root, []state.PersistentVolumeReference{
		{ProjectID: "project", VolumeID: "volume", Kind: state.PersistentVolumeOrdinary, OwnerUID: os.Geteuid(), OwnerGID: os.Getegid()},
	})
	if err == nil {
		t.Fatal("referenced symlink was accepted")
	}
	if content, readErr := os.ReadFile(marker); readErr != nil || string(content) != "safe" {
		t.Fatalf("symlink target was modified: %q, %v", content, readErr)
	}
}

func TestReconcileRemovesUnreferencedSymlinkWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "volumes")
	mustMkdir(t, root, 0o700)
	target := filepath.Join(t.TempDir(), "target")
	mustMkdir(t, target, 0o700)
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "orphan-project")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	result, err := Reconcile(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 {
		t.Fatalf("reconcile result = %+v", result)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("orphan symlink remains: %v", err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "safe" {
		t.Fatalf("symlink target was modified: %q, %v", content, err)
	}
}

func mustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
