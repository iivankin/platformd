package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/iivankin/platformd/internal/state"
)

type imageStorageReferenceStore interface {
	ImageStorageFiles(context.Context) (state.ImageCleanupFiles, error)
}

type imageStorageReconcileResult struct {
	Removed int
}

func reconcileImageStorage(
	ctx context.Context,
	store imageStorageReferenceStore,
	uploadRoot, archiveRoot string,
) (imageStorageReconcileResult, error) {
	if store == nil || !safeImageStorageRoot(uploadRoot) || !safeImageStorageRoot(archiveRoot) || uploadRoot == archiveRoot {
		return imageStorageReconcileResult{}, errors.New("image storage reconcile configuration is invalid")
	}
	for _, root := range []string{uploadRoot, archiveRoot} {
		if err := ensureImageStorageDirectory(root); err != nil {
			return imageStorageReconcileResult{}, err
		}
	}
	files, err := store.ImageStorageFiles(ctx)
	if err != nil {
		return imageStorageReconcileResult{}, err
	}
	uploadReferences, err := imageStorageReferences(uploadRoot, files.UploadPaths)
	if err != nil {
		return imageStorageReconcileResult{}, fmt.Errorf("validate upload paths: %w", err)
	}
	archiveReferences, err := imageStorageReferences(archiveRoot, files.ArchivePaths)
	if err != nil {
		return imageStorageReconcileResult{}, fmt.Errorf("validate archive paths: %w", err)
	}
	result := imageStorageReconcileResult{}
	for root, references := range map[string]map[string]struct{}{
		uploadRoot: uploadReferences, archiveRoot: archiveReferences,
	} {
		removed, err := removeUnreferencedImageFiles(ctx, root, references)
		if err != nil {
			return imageStorageReconcileResult{}, err
		}
		result.Removed += removed
	}
	return result, nil
}

func imageStorageReferences(root string, paths []string) (map[string]struct{}, error) {
	references := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if !imageStoragePathWithin(root, path) {
			return nil, fmt.Errorf("path %q is outside %q", path, root)
		}
		if _, exists := references[path]; exists {
			return nil, fmt.Errorf("path %q is duplicated", path)
		}
		if err := validateImageStoragePath(root, path); err != nil {
			return nil, err
		}
		references[path] = struct{}{}
	}
	return references, nil
}

func validateImageStoragePath(root, path string) error {
	relative, _ := filepath.Rel(root, path)
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("referenced image path %q contains a symlink", path)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("referenced image parent %q is not a directory", current)
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("referenced image path %q is not a regular file", path)
		}
	}
	return nil
}

func removeUnreferencedImageFiles(ctx context.Context, root string, references map[string]struct{}) (int, error) {
	var directories []string
	removed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if _, referenced := references[path]; referenced {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove unreferenced image file %q: %w", path, err)
		}
		removed++
		return nil
	})
	if err != nil {
		return 0, err
	}
	slices.Reverse(directories)
	for _, directory := range directories {
		if err := os.Remove(directory); err != nil && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return 0, fmt.Errorf("remove empty image directory %q: %w", directory, err)
		}
	}
	return removed, nil
}

func ensureImageStorageDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("image storage root %q is not a real directory", path)
	}
	return nil
}

func safeImageStorageRoot(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator)
}

func imageStoragePathWithin(root, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == root {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
