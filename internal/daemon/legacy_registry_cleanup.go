package daemon

import (
	"errors"
	"os"
	"path/filepath"
)

// The v3 schema permanently removes the embedded registry catalog. Its blobs
// have no remaining owners after migration, so leaving the old directory would
// make that disk space unreachable by every later garbage-collection pass.
func removeLegacyRegistryData(dataRoot string) error {
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot || dataRoot == "/" {
		return errors.New("legacy registry cleanup root is unsafe")
	}
	return os.RemoveAll(filepath.Join(dataRoot, "registry"))
}
