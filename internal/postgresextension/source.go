package postgresextension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maximumSourceBytes = 16 << 20

type sourceCache struct {
	root   string
	client *http.Client
}

func (cache sourceCache) ensure(ctx context.Context, recipe Recipe) (string, error) {
	if cache.root == "" || cache.client == nil {
		return "", errors.New("PostgreSQL extension source cache is not configured")
	}
	directory := filepath.Join(cache.root, "sources")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create PostgreSQL extension source cache: %w", err)
	}
	path := filepath.Join(directory, recipe.Name+"-"+recipe.Version+".tar.gz")
	if valid, err := sourceMatches(path, recipe.SourceSHA256); err != nil {
		return "", err
	} else if valid {
		return path, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, recipe.SourceURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := cache.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download PostgreSQL extension source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download PostgreSQL extension source: HTTP %d", response.StatusCode)
	}
	temporary, err := os.CreateTemp(directory, ".source-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, maximumSourceBytes+1))
	if copyErr == nil && written > maximumSourceBytes {
		copyErr = errors.New("PostgreSQL extension source exceeds 16 MiB")
	}
	if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != recipe.SourceSHA256 {
		copyErr = errors.New("PostgreSQL extension source checksum does not match the pinned recipe")
	}
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return "", err
	}
	syncErr := dir.Sync()
	closeErr = dir.Close()
	if syncErr != nil || closeErr != nil {
		return "", errors.Join(syncErr, closeErr)
	}
	return path, nil
}

func sourceMatches(path, expected string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, maximumSourceBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return false, errors.Join(copyErr, closeErr)
	}
	return hex.EncodeToString(hash.Sum(nil)) == expected, nil
}
