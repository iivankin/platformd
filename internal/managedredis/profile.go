package managedredis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const redisConfig = `daemonize no
bind 0.0.0.0
protected-mode yes
port 6379
dir /data
dbfilename dump.rdb
appendonly no
save 300 1
requirepass %s
`

func writeConfig(root, resourceID, password string) (string, error) {
	if !safeRoot(root) || !safePathComponent(resourceID) || !validPassword(password) {
		return "", errors.New("managed Redis generated config input is invalid")
	}
	directory := filepath.Join(root, resourceID)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create managed Redis config directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect managed Redis config directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed Redis config directory is not a directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("set managed Redis config directory mode: %w", err)
	}
	path := filepath.Join(directory, "redis.conf")
	temporary, err := os.CreateTemp(directory, ".redis.conf-")
	if err != nil {
		return "", fmt.Errorf("create managed Redis temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o444); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(temporary, redisConfig, password); err != nil {
		return "", fmt.Errorf("write managed Redis config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync managed Redis config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close managed Redis config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("publish managed Redis config: %w", err)
	}
	committed = true
	return path, nil
}

func ensureVolume(root, projectID, volumeID string) (string, error) {
	if !safeRoot(root) || !safePathComponent(projectID) || !safePathComponent(volumeID) {
		return "", errors.New("managed Redis volume path input is invalid")
	}
	projectRoot := filepath.Join(root, projectID)
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		return "", fmt.Errorf("create project volume directory: %w", err)
	}
	volume := filepath.Join(projectRoot, volumeID)
	if err := os.Mkdir(volume, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create managed Redis volume: %w", err)
	}
	info, err := os.Lstat(volume)
	if err != nil {
		return "", fmt.Errorf("inspect managed Redis volume: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed Redis volume is not a directory")
	}
	return volume, nil
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func safePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, "/\\\x00")
}
