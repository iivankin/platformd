package fsusage

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
)

// DirectoryBytes returns the apparent size of regular files below root.
// Symlinks are intentionally not followed: managed volume paths must not be
// able to make a capacity probe traverse outside the platform data root.
func DirectoryBytes(root string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() < 0 {
			return errors.New("volume file has a negative size")
		}
		size := uint64(info.Size())
		if total > math.MaxUint64-size {
			return errors.New("volume apparent size overflows uint64")
		}
		total += size
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure directory %s: %w", root, err)
	}
	return total, nil
}
