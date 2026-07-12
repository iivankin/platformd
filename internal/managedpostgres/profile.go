package managedpostgres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ensureVolume(root, projectID, volumeID string) (string, error) {
	if !safeRoot(root) || !safePathComponent(projectID) || !safePathComponent(volumeID) {
		return "", errors.New("managed PostgreSQL volume path input is invalid")
	}
	projectRoot := filepath.Join(root, projectID)
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		return "", fmt.Errorf("create project volume directory: %w", err)
	}
	volume := filepath.Join(projectRoot, volumeID)
	if err := os.Mkdir(volume, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create managed PostgreSQL volume: %w", err)
	}
	info, err := os.Lstat(volume)
	if err != nil {
		return "", fmt.Errorf("inspect managed PostgreSQL volume: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed PostgreSQL volume is not a directory")
	}
	// The official entrypoint creates and secures the nested PGDATA itself. The
	// bind root must remain traversable before that drop-to-postgres handoff;
	// its project parent is 0700, while the actual PGDATA becomes 0700.
	if err := os.Chmod(volume, 0o755); err != nil {
		return "", fmt.Errorf("set managed PostgreSQL volume mode: %w", err)
	}
	return volume, nil
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func safePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, `/\\\x00`)
}
