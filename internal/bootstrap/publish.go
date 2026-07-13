package bootstrap

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/releasebundle"
	"github.com/iivankin/platformd/internal/releaseconfig"
	"github.com/iivankin/platformd/internal/releasemanifest"
)

func PublishReleaseSlot(release VerifiedRelease, paths layout.Paths, expectedUID int) error {
	if err := ensurePrivateDirectory(paths.ReleasesRoot, expectedUID); err != nil {
		return err
	}
	target := filepath.Join(paths.ReleasesRoot, release.Manifest.Version)
	if _, err := os.Lstat(target); err == nil {
		return VerifyReleaseSlot(target, release.ManifestBytes, release.PublicKey, expectedUID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(paths.ReleasesRoot, ".staging-")
	if err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return err
	}
	if err := copyRegularFile(release.ExecutablePath, filepath.Join(staging, "platformd"), 0o755, release.Manifest.BinarySize); err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(staging, "release-manifest.json"), release.ManifestBytes, 0o644); err != nil {
		return err
	}
	if err := release.ExtractRuntime(staging); err != nil {
		return err
	}
	if err := syncDirectoryTree(staging); err != nil {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return VerifyReleaseSlot(target, release.ManifestBytes, release.PublicKey, expectedUID)
		}
		return fmt.Errorf("publish release slot: %w", err)
	}
	if err := syncDirectory(paths.ReleasesRoot); err != nil {
		return err
	}
	return VerifyReleaseSlot(target, release.ManifestBytes, release.PublicKey, expectedUID)
}

func VerifyReleaseSlot(slot string, expectedManifest []byte, publicKey ed25519.PublicKey, expectedUID int) error {
	if err := verifyDirectory(slot, 0o700, expectedUID); err != nil {
		return err
	}
	entries, err := os.ReadDir(slot)
	if err != nil {
		return err
	}
	allowed := map[string]struct{}{"platformd": {}, "release-manifest.json": {}, "runtime": {}}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return fmt.Errorf("release slot contains unexpected entry %q", entry.Name())
		}
	}
	if len(entries) != len(allowed) {
		return errors.New("release slot is incomplete")
	}
	manifestPath := filepath.Join(slot, "release-manifest.json")
	if err := verifyRegularFile(manifestPath, 0o644, expectedUID); err != nil {
		return err
	}
	value, err := readBoundedFile(manifestPath, maximumReleaseManifestBytes)
	if err != nil {
		return fmt.Errorf("read installed release manifest: %w", err)
	}
	if expectedManifest != nil && !bytes.Equal(value, expectedManifest) {
		return errors.New("installed release manifest differs from verified manifest")
	}
	if publicKey == nil {
		publicKey, err = releaseconfig.PublicKey()
		if err != nil {
			return err
		}
	}
	manifest, err := releasemanifest.ParseAndVerify(value, publicKey)
	if err != nil {
		return err
	}
	binaryPath := filepath.Join(slot, "platformd")
	if manifest.Version != filepath.Base(slot) {
		return errors.New("release slot directory does not match manifest version")
	}
	if err := verifyRegularFile(binaryPath, 0o755, expectedUID); err != nil {
		return err
	}
	if err := manifest.VerifyBinary(binaryPath); err != nil {
		return err
	}
	bundle, err := releasebundle.Open(binaryPath)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if err := bundle.Verify(); err != nil {
		return err
	}
	return bundle.VerifyExtracted(slot)
}

func VerifyCurrentRelease(paths layout.Paths, publicKey ed25519.PublicKey, expectedUID int) error {
	_, err := CurrentReleaseManifest(paths, publicKey, expectedUID)
	return err
}

func CurrentReleaseManifest(paths layout.Paths, publicKey ed25519.PublicKey, expectedUID int) (releasemanifest.Manifest, error) {
	info, err := os.Lstat(paths.Current)
	if err != nil {
		return releasemanifest.Manifest{}, fmt.Errorf("inspect current release link: %w", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return releasemanifest.Manifest{}, errors.New("current release path is not a symlink")
	}
	target, err := os.Readlink(paths.Current)
	if err != nil {
		return releasemanifest.Manifest{}, err
	}
	if !validReleaseName(target) {
		return releasemanifest.Manifest{}, errors.New("current release link target is invalid")
	}
	slot := filepath.Join(paths.ReleasesRoot, target)
	if err := VerifyReleaseSlot(slot, nil, publicKey, expectedUID); err != nil {
		return releasemanifest.Manifest{}, err
	}
	value, err := readBoundedFile(filepath.Join(slot, "release-manifest.json"), maximumReleaseManifestBytes)
	if err != nil {
		return releasemanifest.Manifest{}, err
	}
	if publicKey == nil {
		publicKey, err = releaseconfig.PublicKey()
		if err != nil {
			return releasemanifest.Manifest{}, err
		}
	}
	return releasemanifest.ParseAndVerify(value, publicKey)
}

func SwitchCurrentRelease(paths layout.Paths, version string) error {
	if !validReleaseName(version) {
		return errors.New("release version is not a safe slot name")
	}
	return replaceSymlink(version, paths.Current)
}

// SwitchToRelease verifies both slots and publishes previous before the single
// durable selection boundary, current. A crash before current changes leaves
// the old daemon selected; a crash after it starts the new slot.
func SwitchToRelease(paths layout.Paths, expectedCurrent, target string, publicKey ed25519.PublicKey, expectedUID int) error {
	if !validReleaseName(expectedCurrent) || !validReleaseName(target) || expectedCurrent == target {
		return errors.New("release switch versions are invalid")
	}
	current, err := os.Readlink(paths.Current)
	if err != nil {
		return fmt.Errorf("read current release link: %w", err)
	}
	if current != expectedCurrent {
		return errors.New("current release changed while update was staged")
	}
	if err := VerifyReleaseSlot(filepath.Join(paths.ReleasesRoot, expectedCurrent), nil, publicKey, expectedUID); err != nil {
		return fmt.Errorf("verify previous release slot: %w", err)
	}
	if err := VerifyReleaseSlot(filepath.Join(paths.ReleasesRoot, target), nil, publicKey, expectedUID); err != nil {
		return fmt.Errorf("verify target release slot: %w", err)
	}
	if err := replaceSymlink(expectedCurrent, paths.Previous); err != nil {
		return fmt.Errorf("publish previous release link: %w", err)
	}
	if err := SwitchCurrentRelease(paths, target); err != nil {
		return fmt.Errorf("switch current release: %w", err)
	}
	return nil
}

// FinalizeSuccessfulUpdate is called only after control-plane readiness. At
// that point rollback is no longer offered, so every non-current slot and
// transient download can be removed without another durable marker.
func FinalizeSuccessfulUpdate(paths layout.Paths, publicKey ed25519.PublicKey, expectedUID int) error {
	current, err := os.Readlink(paths.Current)
	if err != nil || !validReleaseName(current) {
		return errors.New("current release link is invalid during readiness cleanup")
	}
	if err := VerifyReleaseSlot(filepath.Join(paths.ReleasesRoot, current), nil, publicKey, expectedUID); err != nil {
		return err
	}
	if info, err := os.Lstat(paths.Previous); err == nil {
		if info.Mode()&fs.ModeSymlink == 0 {
			return errors.New("previous release path is not a symlink")
		}
		previous, err := os.Readlink(paths.Previous)
		if err != nil || !validReleaseName(previous) || previous == current {
			return errors.New("previous release link is invalid")
		}
		if err := os.Remove(paths.Previous); err != nil {
			return err
		}
		if err := syncDirectory(paths.ReleasesRoot); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	entries, err := os.ReadDir(paths.ReleasesRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "current" || entry.Name() == current {
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("release root contains unexpected entry %q", entry.Name())
		}
		if !entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".download-") && entry.Type().IsRegular() {
				if err := os.Remove(filepath.Join(paths.ReleasesRoot, entry.Name())); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("release root contains unexpected entry %q", entry.Name())
		}
		if err := os.RemoveAll(filepath.Join(paths.ReleasesRoot, entry.Name())); err != nil {
			return fmt.Errorf("remove obsolete release %q: %w", entry.Name(), err)
		}
	}
	return syncDirectory(paths.ReleasesRoot)
}

func validReleaseName(value string) bool {
	return value != "" && !filepath.IsAbs(value) && filepath.Base(value) == value && value != "." && value != ".."
}

func installLocalBinaryLink(paths layout.Paths) error {
	return replaceSymlink(filepath.Join(paths.Current, "platformd"), paths.LocalBinary)
}

func replaceSymlink(target, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".platformd-link-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := os.Symlink(target, temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func ensurePrivateDirectory(path string, expectedUID int) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory %s: %w", path, err)
	}
	return verifyDirectory(path, 0o700, expectedUID)
}

func verifyDirectory(path string, mode fs.FileMode, expectedUID int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != mode {
		return fmt.Errorf("directory %s type/mode is unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != expectedUID {
		return fmt.Errorf("directory %s ownership is unsafe", path)
	}
	return nil
}

func copyRegularFile(source, destination string, mode fs.FileMode, expectedSize int64) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedSize {
		return errors.New("release source binary is not the verified regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	chmodErr := output.Chmod(mode)
	written, copyErr := io.Copy(output, io.LimitReader(input, expectedSize+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	if chmodErr != nil || copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(chmodErr, copyErr, syncErr, closeErr)
	}
	if written != expectedSize {
		return errors.New("release source binary changed while copying")
	}
	return nil
}

func writeNewFile(path string, value []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	chmodErr := file.Chmod(mode)
	_, writeErr := file.Write(value)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(chmodErr, writeErr, syncErr, closeErr)
}

func verifyRegularFile(path string, mode fs.FileMode, expectedUID int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return fmt.Errorf("file %s type/mode is unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != expectedUID {
		return fmt.Errorf("file %s ownership is unsafe", path)
	}
	return nil
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, errors.New("file exceeds size limit")
	}
	return value, nil
}

func syncDirectoryTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(value string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, value)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDirectory(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
