package buildlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	MaxBytes         = 8 << 20
	TruncationMarker = "\n[platformd] Build log truncated at 8 MiB.\n"
)

type cappedAppendWriter struct {
	file      *os.File
	remaining int64
	truncated bool
}

func OpenAppend(path string) (io.WriteCloser, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create build log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open build log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect build log: %w", err)
	}
	dataLimit := int64(MaxBytes - len(TruncationMarker))
	if info.Size() > dataLimit && info.Size() < MaxBytes {
		if err := file.Truncate(dataLimit); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("cap existing build log: %w", err)
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("seek existing build log: %w", err)
		}
		info, err = file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("inspect capped build log: %w", err)
		}
	}
	remaining := dataLimit - info.Size()
	if remaining < 0 {
		remaining = 0
	}
	return &cappedAppendWriter{
		file: file, remaining: remaining, truncated: info.Size() >= MaxBytes,
	}, nil
}

func Append(path, content string) error {
	writer, err := OpenAppend(path)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(writer, content)
	return errors.Join(writeErr, writer.Close())
}

func (writer *cappedAppendWriter) Write(content []byte) (int, error) {
	requested := len(content)
	if requested == 0 {
		return requested, nil
	}
	if writer.remaining == 0 {
		if !writer.truncated {
			if _, err := io.WriteString(writer.file, TruncationMarker); err != nil {
				return 0, err
			}
			writer.truncated = true
		}
		return requested, nil
	}
	if int64(requested) <= writer.remaining {
		written, err := writer.file.Write(content)
		writer.remaining -= int64(written)
		return written, err
	}
	prefix := content[:writer.remaining]
	if _, err := writer.file.Write(prefix); err != nil {
		return 0, err
	}
	writer.remaining = 0
	if !writer.truncated {
		if _, err := io.WriteString(writer.file, TruncationMarker); err != nil {
			return 0, err
		}
		writer.truncated = true
	}
	// Returning the requested length keeps a verbose build process running even
	// after platformd has deliberately stopped persisting additional output.
	return requested, nil
}

func (writer *cappedAppendWriter) Close() error {
	return writer.file.Close()
}
