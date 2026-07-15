//go:build linux && amd64 && cgo

package containerengine

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/cyphar/filepath-securejoin/pathrs-lite"
	"golang.org/x/sys/unix"
)

const maximumContainerFileEntries = 10_000
const containerFileListingDepth = 3

func (e *Engine) ListContainerFiles(ctx context.Context, containerID, rootPath string) (entries []ContainerFileEntry, returnErr error) {
	if err := validateContainerFilePath(rootPath); err != nil {
		return nil, err
	}
	container, err := e.lookupContainer(containerID)
	if err != nil {
		return nil, err
	}
	mountpoint, err := container.Mount()
	if err != nil {
		return nil, fmt.Errorf("mount container %s rootfs: %w", containerID, err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, container.Unmount(false))
	}()

	entries = make([]ContainerFileEntry, 0, 256)
	if err := walkContainerDirectory(ctx, mountpoint, rootPath, 0, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func walkContainerDirectory(ctx context.Context, mountpoint, directoryPath string, depth int, result *[]ContainerFileEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := pathrs.OpenInRoot(mountpoint, directoryPath)
	if err != nil {
		return fmt.Errorf("open container directory %s: %w", directoryPath, err)
	}
	defer handle.Close()
	directory, err := pathrs.Reopen(handle, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return fmt.Errorf("read container directory %s: %w", directoryPath, err)
	}
	defer directory.Close()
	children, err := directory.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list container directory %s: %w", directoryPath, err)
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(*result) >= maximumContainerFileEntries {
			return fmt.Errorf("container file tree exceeds %d entries", maximumContainerFileEntries)
		}
		childPath := path.Join(directoryPath, child.Name())
		info, err := child.Info()
		if err != nil {
			return fmt.Errorf("inspect container path %s: %w", childPath, err)
		}
		*result = append(*result, ContainerFileEntry{
			Path: childPath, Directory: child.IsDir(), SizeBytes: info.Size(),
			Mode: uint32(info.Mode().Perm()), ModifiedAt: info.ModTime(),
		})
		// Symlinks are represented as leaf entries. Following them while the
		// container is mutating would make the visible tree ambiguous.
		// Bound each response so large image filesystems remain usable. A caller
		// can use any returned directory as the root of the next listing.
		if depth < containerFileListingDepth && child.IsDir() && child.Type()&os.ModeSymlink == 0 {
			if err := walkContainerDirectory(ctx, mountpoint, childPath, depth+1, result); err != nil {
				return err
			}
		}
	}
	return nil
}

type mountedContainerFile struct {
	*os.File
	unmount   func() error
	closeOnce sync.Once
	closeErr  error
}

func (file *mountedContainerFile) Close() error {
	file.closeOnce.Do(func() {
		file.closeErr = errors.Join(file.File.Close(), file.unmount())
	})
	return file.closeErr
}

func (e *Engine) OpenContainerFile(ctx context.Context, containerID, filePath string) (io.ReadCloser, ContainerFileEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, ContainerFileEntry{}, err
	}
	if err := validateContainerFilePath(filePath); err != nil {
		return nil, ContainerFileEntry{}, err
	}
	container, err := e.lookupContainer(containerID)
	if err != nil {
		return nil, ContainerFileEntry{}, err
	}
	mountpoint, err := container.Mount()
	if err != nil {
		return nil, ContainerFileEntry{}, fmt.Errorf("mount container %s rootfs: %w", containerID, err)
	}
	unmount := func() error { return container.Unmount(false) }
	handle, err := pathrs.OpenInRoot(mountpoint, filePath)
	if err != nil {
		_ = unmount()
		return nil, ContainerFileEntry{}, fmt.Errorf("open container file %s: %w", filePath, err)
	}
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = handle.Close()
		_ = unmount()
		return nil, ContainerFileEntry{}, errors.Join(err, errors.New("container download path is not a regular file"))
	}
	opened, err := pathrs.Reopen(handle, unix.O_RDONLY)
	_ = handle.Close()
	if err != nil {
		_ = unmount()
		return nil, ContainerFileEntry{}, fmt.Errorf("read container file %s: %w", filePath, err)
	}
	return &mountedContainerFile{File: opened, unmount: unmount}, ContainerFileEntry{
		Path: filePath, SizeBytes: info.Size(), Mode: uint32(info.Mode().Perm()), ModifiedAt: info.ModTime(),
	}, nil
}

func (e *Engine) WriteContainerFile(ctx context.Context, containerID, filePath string, source io.Reader, sizeBytes int64) error {
	if err := validateContainerFilePath(filePath); err != nil {
		return err
	}
	if source == nil || sizeBytes < 0 {
		return errors.New("container upload source and size are invalid")
	}
	container, err := e.lookupContainer(containerID)
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	go func() {
		archive := tar.NewWriter(writer)
		headerErr := archive.WriteHeader(&tar.Header{
			Name: path.Base(filePath), Mode: 0o640, Size: sizeBytes,
		})
		if headerErr == nil {
			_, headerErr = io.CopyN(archive, source, sizeBytes)
		}
		closeErr := archive.Close()
		_ = writer.CloseWithError(errors.Join(headerErr, closeErr))
	}()
	copy, err := container.CopyFromArchive(ctx, path.Dir(filePath), true, true, nil, reader)
	if err != nil {
		_ = reader.Close()
		return fmt.Errorf("prepare container file upload: %w", err)
	}
	if err := copy(); err != nil {
		return fmt.Errorf("upload container file %s: %w", filePath, err)
	}
	return nil
}

func validateContainerFilePath(value string) error {
	if value == "" || !strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsRune(value, '\x00') {
		return errors.New("container file path must be canonical and absolute")
	}
	return nil
}
