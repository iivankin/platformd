package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

type imageStorageReferencesStub struct {
	files state.ImageCleanupFiles
}

func (stub imageStorageReferencesStub) ImageStorageFiles(context.Context) (state.ImageCleanupFiles, error) {
	return stub.files, nil
}

func TestImageStorageReconcileRemovesOnlyUnreferencedFiles(t *testing.T) {
	root := t.TempDir()
	uploadRoot := filepath.Join(root, "uploads")
	archiveRoot := filepath.Join(root, "images")
	upload := filepath.Join(uploadRoot, "upload.part")
	archive := filepath.Join(archiveRoot, "service", "revision.oci")
	orphanUpload := filepath.Join(uploadRoot, "orphan.part")
	orphanArchive := filepath.Join(archiveRoot, "service", "orphan.oci")
	for path, content := range map[string]string{
		upload: "upload", archive: "archive", orphanUpload: "orphan", orphanArchive: "orphan",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	external := filepath.Join(root, "external")
	if err := os.WriteFile(external, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(archiveRoot, "orphan-link")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}

	result, err := reconcileImageStorage(context.Background(), imageStorageReferencesStub{files: state.ImageCleanupFiles{
		UploadPaths: []string{upload}, ArchivePaths: []string{archive},
	}}, uploadRoot, archiveRoot)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 3 {
		t.Fatalf("reconcile result = %+v", result)
	}
	for _, preserved := range []string{upload, archive, external} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("referenced file %q was removed: %v", preserved, err)
		}
	}
	for _, removed := range []string{orphanUpload, orphanArchive, link} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("orphan %q remains: %v", removed, err)
		}
	}
}

func TestImageStorageReconcileRejectsReferenceOutsideRoot(t *testing.T) {
	root := t.TempDir()
	_, err := reconcileImageStorage(context.Background(), imageStorageReferencesStub{files: state.ImageCleanupFiles{
		UploadPaths: []string{filepath.Join(root, "outside.part")},
	}}, filepath.Join(root, "uploads"), filepath.Join(root, "images"))
	if err == nil {
		t.Fatal("outside image path was accepted")
	}
}
