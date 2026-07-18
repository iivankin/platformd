package volume

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

const maximumArchivePathBytes = 4096
const maximumArchiveOwnerID = int64(1<<32 - 2)

func OpenLiveBackup(ctx context.Context, root string, volume state.Volume) (io.ReadCloser, error) {
	path, err := ordinaryVolumePath(root, volume)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("ordinary volume path is not a directory")
	}
	reader, writer := io.Pipe()
	go func() {
		archive := tar.NewWriter(writer)
		walkErr := filepath.WalkDir(path, func(itemPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if itemPath == path {
				return nil
			}
			relative, err := filepath.Rel(path, itemPath)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(relative)
			if err := validateArchiveName(name); err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("volume contains unsupported special file %q", name)
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(itemPath)
				if err != nil {
					return err
				}
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			header.Name = name
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			file, err := os.Open(itemPath)
			if err != nil {
				return err
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !os.SameFile(info, openedInfo) {
				_ = file.Close()
				return errors.New("volume file changed identity while it was being backed up")
			}
			_, copyErr := io.CopyN(archive, file, info.Size())
			closeErr := file.Close()
			return errors.Join(copyErr, closeErr)
		})
		closeErr := archive.Close()
		_ = writer.CloseWithError(errors.Join(walkErr, closeErr))
	}()
	return reader, nil
}

func RestoreBackup(ctx context.Context, root string, volume state.Volume, source io.Reader) error {
	live, err := ordinaryVolumePath(root, volume)
	if err != nil {
		return err
	}
	parent := filepath.Dir(live)
	staging, err := os.MkdirTemp(parent, "."+volume.ID+"-restore-")
	if err != nil {
		return err
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Chown(staging, volume.OwnerUID, volume.OwnerGID); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	stagingLive := true
	defer func() {
		if stagingLive {
			_ = os.RemoveAll(staging)
		}
	}()
	archive := tar.NewReader(source)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := restoreArchiveEntry(staging, header, archive); err != nil {
			return err
		}
	}
	if err := syncTree(staging); err != nil {
		return err
	}
	previous := live + ".previous"
	if err := os.RemoveAll(previous); err != nil {
		return err
	}
	if err := os.Rename(live, previous); err != nil {
		return err
	}
	if err := os.Rename(staging, live); err != nil {
		return errors.Join(err, os.Rename(previous, live))
	}
	stagingLive = false
	if err := syncDirectory(parent); err != nil {
		return err
	}
	return os.RemoveAll(previous)
}

func restoreArchiveEntry(root string, header *tar.Header, source io.Reader) error {
	if header == nil || validateArchiveName(header.Name) != nil {
		return errors.New("volume backup contains an invalid path")
	}
	if header.Uid < 0 || int64(header.Uid) > maximumArchiveOwnerID ||
		header.Gid < 0 || int64(header.Gid) > maximumArchiveOwnerID {
		return errors.New("volume backup contains an invalid owner")
	}
	destination := filepath.Join(root, filepath.FromSlash(header.Name))
	if err := ensureSafeParents(root, filepath.Dir(destination)); err != nil {
		return err
	}
	mode := fs.FileMode(header.Mode) & fs.ModePerm
	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(destination, mode); err != nil {
			return err
		}
	case tar.TypeReg, tar.TypeRegA:
		if header.Size < 0 {
			return errors.New("volume backup contains a negative file size")
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, source, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			return err
		}
	case tar.TypeSymlink:
		if strings.ContainsRune(header.Linkname, 0) {
			return errors.New("volume backup contains an invalid symbolic link")
		}
		if err := os.Symlink(header.Linkname, destination); err != nil {
			return err
		}
	default:
		return errors.New("volume backup contains an unsupported file type")
	}
	if header.Typeflag == tar.TypeSymlink {
		return os.Lchown(destination, header.Uid, header.Gid)
	}
	return os.Chown(destination, header.Uid, header.Gid)
}

func validateArchiveName(name string) error {
	if name == "" || len(name) > maximumArchivePathBytes || strings.ContainsRune(name, 0) ||
		strings.HasPrefix(name, "/") || filepath.IsAbs(filepath.FromSlash(name)) {
		return errors.New("archive path is invalid")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("archive path escapes the volume")
	}
	return nil
}

func ensureSafeParents(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("archive parent escapes the volume")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive path traverses a non-directory")
		}
	}
	return nil
}

func ordinaryVolumePath(root string, volume state.Volume) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) ||
		!safeArchiveComponent(volume.ID) || !safeArchiveComponent(volume.ProjectID) {
		return "", errors.New("ordinary volume backup identity is invalid")
	}
	return filepath.Join(root, volume.ProjectID, volume.ID), nil
}

func safeArchiveComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value
}

func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		return errors.Join(syncErr, closeErr)
	})
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
