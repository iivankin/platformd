//go:build linux

package singletonlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrAlreadyRunning = errors.New("platformd is already running")

type Lock struct {
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

func Acquire(path string, expectedUID int) (*Lock, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("daemon lock path %q is not canonical and absolute", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create daemon lock directory: %w", err)
	}
	descriptor, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	cleanup := func(inner error) (*Lock, error) {
		return nil, errors.Join(inner, file.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return cleanup(fmt.Errorf("inspect daemon lock: %w", err))
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || int(stat.Uid) != expectedUID {
		return cleanup(errors.New("daemon lock ownership or mode is unsafe"))
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return cleanup(ErrAlreadyRunning)
		}
		return cleanup(fmt.Errorf("acquire daemon lock: %w", err))
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Close() error {
	lock.closeOnce.Do(func() {
		lock.closeErr = errors.Join(
			unix.Flock(int(lock.file.Fd()), unix.LOCK_UN),
			lock.file.Close(),
		)
	})
	return lock.closeErr
}
