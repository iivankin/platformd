package managedpostgres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	legacyVolumeDestination = "/var/lib/postgresql/data"
	legacyPGData            = "/var/lib/postgresql/data/pgdata"
	versionedVolumeRoot     = "/var/lib/postgresql"
)

type storageProfile struct {
	volumeDestination string
	pgData            string
}

func storageProfileForTag(tag string) storageProfile {
	majorEnd := 0
	for majorEnd < len(tag) && tag[majorEnd] >= '0' && tag[majorEnd] <= '9' {
		majorEnd++
	}
	major, err := strconv.Atoi(tag[:majorEnd])
	if err == nil && major >= 18 {
		// PostgreSQL 18 moved the image volume to the parent directory so that
		// major-version upgrades can keep separate clusters below one mount.
		// Mounting the legacy child path is shadowed by the image's parent VOLUME.
		return storageProfile{
			volumeDestination: versionedVolumeRoot,
			pgData:            filepath.Join(versionedVolumeRoot, strconv.Itoa(major), "docker"),
		}
	}
	return storageProfile{volumeDestination: legacyVolumeDestination, pgData: legacyPGData}
}

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
	return volume, nil
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}

func safePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value &&
		!strings.ContainsAny(value, "/\\\x00")
}
