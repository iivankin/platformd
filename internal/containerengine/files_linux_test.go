//go:build linux && amd64 && cgo

package containerengine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListContainerDirectoryReturnsOnlyImmediateChildren(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, directory := range []string{"app", "app/cache", "app/cache/nested"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"app/config.json", "app/cache/item", "app/cache/nested/deep"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	var entries []ContainerFileEntry
	if err := listContainerDirectory(context.Background(), root, "/app", &entries); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/app/cache", "/app/config.json"}) {
		t.Fatalf("immediate entries = %v", paths)
	}

	entries = nil
	if err := listContainerDirectory(context.Background(), root, "/app/cache", &entries); err != nil {
		t.Fatal(err)
	}
	paths = paths[:0]
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/app/cache/item", "/app/cache/nested"}) {
		t.Fatalf("nested entries = %v", paths)
	}
}
