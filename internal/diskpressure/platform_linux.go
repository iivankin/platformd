//go:build linux

package diskpressure

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type StatfsCollector struct{}

func (StatfsCollector) Collect(path string) (Usage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return Usage{}, fmt.Errorf("statfs platform data root: %w", err)
	}
	if stat.Bsize <= 0 {
		return Usage{}, errors.New("statfs returned an invalid block size")
	}
	blockSize := uint64(stat.Bsize)
	totalBytes, err := multiply(stat.Blocks, blockSize)
	if err != nil {
		return Usage{}, err
	}
	availableBytes, err := multiply(stat.Bavail, blockSize)
	if err != nil {
		return Usage{}, err
	}
	return Usage{
		TotalBytes: totalBytes, AvailableBytes: availableBytes,
		TotalInodes: stat.Files, AvailableInodes: stat.Ffree,
	}, nil
}

func multiply(left, right uint64) (uint64, error) {
	if right != 0 && left > math.MaxUint64/right {
		return 0, errors.New("statfs capacity overflow")
	}
	return left * right, nil
}

type FileReserve struct {
	expectedUID int
}

func NewFileReserve(expectedUID int) (*FileReserve, error) {
	if expectedUID < 0 {
		return nil, errors.New("reserve expected UID is invalid")
	}
	return &FileReserve{expectedUID: expectedUID}, nil
}

func (reserve *FileReserve) Present(path string, expectedSize int64) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return false, errors.New("disk reserve file type or mode is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != reserve.expectedUID {
		return false, errors.New("disk reserve file ownership is unsafe")
	}
	return info.Size() == expectedSize, nil
}

func (reserve *FileReserve) Ensure(path string, size int64) error {
	if size <= 0 {
		return errors.New("disk reserve size must be positive")
	}
	present, err := reserve.Present(path, size)
	if err != nil || present {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".reserve-")
	if err != nil {
		return fmt.Errorf("create temporary disk reserve: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	chmodErr := temporary.Chmod(0o600)
	fallocateErr := unix.Fallocate(int(temporary.Fd()), 0, 0, size)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if chmodErr != nil || fallocateErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("preallocate disk reserve: %w", errors.Join(chmodErr, fallocateErr, syncErr, closeErr))
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish disk reserve: %w", err)
	}
	return syncDirectory(directory)
}

func (reserve *FileReserve) Remove(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("refusing to remove unsafe disk reserve path")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != reserve.expectedUID {
		return errors.New("refusing to remove disk reserve with unsafe ownership")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
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
