//go:build linux

package cgroupstats

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type memoryPeakReader struct {
	mu          sync.Mutex
	files       map[string]*os.File
	unsupported bool
}

func newMemoryPeakReader() *memoryPeakReader {
	return &memoryPeakReader{files: make(map[string]*os.File)}
}

func (reader *memoryPeakReader) read(resourceRoot, key string, current uint64) uint64 {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.unsupported {
		return current
	}
	filename := filepath.Join(resourceRoot, "memory.peak")
	file := reader.files[key]
	if file != nil && !sameFile(file, filename) {
		_ = file.Close()
		delete(reader.files, key)
		file = nil
	}
	if file == nil {
		var err error
		file, err = os.OpenFile(filename, os.O_RDWR|os.O_APPEND, 0)
		if err != nil {
			if unsupportedPeakReset(err) {
				reader.disable()
			}
			return current
		}
		reader.files[key] = file
	}
	peak, err := readAndResetMemoryPeak(file)
	if err != nil {
		_ = file.Close()
		delete(reader.files, key)
		if unsupportedPeakReset(err) {
			reader.disable()
		}
		return current
	}
	return max(peak, current)
}

func (reader *memoryPeakReader) forget(key string) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if file := reader.files[key]; file != nil {
		_ = file.Close()
		delete(reader.files, key)
	}
}

func (reader *memoryPeakReader) close() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	var failures []error
	for key, file := range reader.files {
		failures = append(failures, file.Close())
		delete(reader.files, key)
	}
	return errors.Join(failures...)
}

func (reader *memoryPeakReader) disable() {
	reader.unsupported = true
	for key, file := range reader.files {
		_ = file.Close()
		delete(reader.files, key)
	}
}

func sameFile(file *os.File, filename string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(filename)
	return err == nil && os.SameFile(opened, current)
}

func readAndResetMemoryPeak(file *os.File) (uint64, error) {
	var value [64]byte
	n, err := file.ReadAt(value[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	peak, err := strconv.ParseUint(strings.TrimSpace(string(value[:n])), 10, 64)
	if err != nil {
		return 0, err
	}
	// Since Linux 6.12, a non-empty write resets memory.peak only for
	// subsequent reads through this same FD. Keeping it open and using append
	// mode matches the kernel selftest for this cgroup interface.
	if _, err := file.WriteString("1"); err != nil {
		return 0, err
	}
	return peak, nil
}

func unsupportedPeakReset(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, unix.EROFS) ||
		errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP)
}
