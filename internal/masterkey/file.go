package masterkey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/iivankin/platformd/internal/cryptobox"
)

const fileMode = 0o600

func Load(path string, expectedUID int) (cryptobox.MasterKey, error) {
	return load(path, expectedUID)
}

func LoadOrCreate(path string, expectedUID int, random io.Reader) (cryptobox.MasterKey, bool, error) {
	key, err := load(path, expectedUID)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return cryptobox.MasterKey{}, false, err
	}

	var value [32]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return cryptobox.MasterKey{}, false, fmt.Errorf("generate master key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadExistingAfterRace(path, expectedUID)
		}
		return cryptobox.MasterKey{}, false, fmt.Errorf("create master key: %w", err)
	}
	created := true
	defer func() {
		_ = file.Close()
	}()
	if _, err := file.Write(value[:]); err != nil {
		return cryptobox.MasterKey{}, created, fmt.Errorf("write master key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return cryptobox.MasterKey{}, created, fmt.Errorf("sync master key: %w", err)
	}
	if err := file.Close(); err != nil {
		return cryptobox.MasterKey{}, created, fmt.Errorf("close master key: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return cryptobox.MasterKey{}, created, err
	}
	key, err = cryptobox.ParseMasterKey(value[:])
	return key, created, err
}

func loadExistingAfterRace(path string, expectedUID int) (cryptobox.MasterKey, bool, error) {
	key, err := load(path, expectedUID)
	return key, false, err
}

func load(path string, expectedUID int) (cryptobox.MasterKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return cryptobox.MasterKey{}, err
	}
	if !info.Mode().IsRegular() {
		return cryptobox.MasterKey{}, errors.New("master key path is not a regular file")
	}
	if info.Mode().Perm() != fileMode {
		return cryptobox.MasterKey{}, fmt.Errorf("master key mode = %04o, want 0600", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return cryptobox.MasterKey{}, errors.New("master key ownership is unavailable")
	}
	if int(stat.Uid) != expectedUID {
		return cryptobox.MasterKey{}, fmt.Errorf("master key uid = %d, want %d", stat.Uid, expectedUID)
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return cryptobox.MasterKey{}, fmt.Errorf("read master key: %w", err)
	}
	return cryptobox.ParseMasterKey(value)
}

func RecoveryString(key cryptobox.MasterKey) string {
	return base64.RawURLEncoding.EncodeToString(key[:])
}

func ParseRecoveryString(value string) (cryptobox.MasterKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cryptobox.MasterKey{}, errors.New("master recovery key is invalid")
	}
	defer clear(decoded)
	key, err := cryptobox.ParseMasterKey(decoded)
	if err != nil {
		return cryptobox.MasterKey{}, errors.New("master recovery key is invalid")
	}
	return key, nil
}

// Install writes a supplied recovery key exactly once. A retry after a crash
// accepts only the same key; it never replaces an existing installation key.
func Install(path string, expectedUID int, key cryptobox.MasterKey) error {
	if existing, err := load(path, expectedUID); err == nil {
		if existing != key {
			return errors.New("existing master key differs from supplied recovery key")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, loadErr := load(path, expectedUID)
			if loadErr == nil && existing == key {
				return nil
			}
			return errors.Join(err, loadErr)
		}
		return err
	}
	_, writeErr := file.Write(key[:])
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open master key directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync master key directory: %w", err)
	}
	return nil
}
