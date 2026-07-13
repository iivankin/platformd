package registry

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/registryname"
)

var ErrBlobDigestMismatch = errors.New("registry blob digest does not match content")

type PayloadStore struct {
	root string
}

type BlobStats struct {
	Count int
	Bytes int64
}

type TemporaryUpload struct {
	RepositoryID string
	UploadID     string
	Size         int64
	ModifiedAt   time.Time
}

type BlobInfo struct {
	Digest     string
	Size       int64
	ModifiedAt time.Time
}

func NewPayloadStore(root string) (*PayloadStore, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return nil, errors.New("registry payload root must be a canonical absolute non-root path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &PayloadStore{root: root}, nil
}

func (store *PayloadStore) BeginUpload(repositoryID, uploadID string) error {
	uploads, err := store.uploadRoot(repositoryID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(uploads, uploadID+".part"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return errors.Join(file.Close(), syncDirectory(uploads))
}

func (store *PayloadStore) Append(ctx context.Context, repositoryID, uploadID string, input io.Reader) (int64, error) {
	path, err := store.uploadPath(repositoryID, uploadID)
	if err != nil {
		return 0, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return 0, errors.Join(err, errors.New("registry upload payload is not a regular file"))
	}
	initialSize := info.Size()
	_, copyErr := io.Copy(file, &contextReader{ctx: ctx, input: input})
	if copyErr == nil {
		copyErr = file.Sync()
	}
	var rollbackErr error
	if copyErr != nil {
		// A failed request must not leave a partial chunk that a client would resend.
		rollbackErr = file.Truncate(initialSize)
		if rollbackErr == nil {
			rollbackErr = file.Sync()
		}
	}
	closeErr := file.Close()
	if copyErr != nil || rollbackErr != nil || closeErr != nil {
		return 0, errors.Join(copyErr, rollbackErr, closeErr)
	}
	return store.UploadSize(repositoryID, uploadID)
}

func (store *PayloadStore) TemporaryBytes() (int64, error) {
	var total int64
	err := filepath.WalkDir(store.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Base(filepath.Dir(path)) != "uploads" || !strings.HasSuffix(entry.Name(), ".part") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("registry temporary payload is not a regular file")
		}
		if info.Size() > math.MaxInt64-total {
			return errors.New("registry temporary payload size overflow")
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (store *PayloadStore) UploadSize(repositoryID, uploadID string) (int64, error) {
	path, err := store.uploadPath(repositoryID, uploadID)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("registry upload payload is not a regular file")
	}
	return info.Size(), nil
}

func (store *PayloadStore) Finalize(ctx context.Context, repositoryID, uploadID, expectedDigest string, finalChunk io.Reader) (int64, error) {
	if err := registryname.ValidateDigest(expectedDigest); err != nil {
		return 0, err
	}
	if finalChunk != nil {
		if _, err := store.Append(ctx, repositoryID, uploadID, finalChunk); err != nil {
			return 0, err
		}
	}
	uploadPath, err := store.uploadPath(repositoryID, uploadID)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(uploadPath)
	if err != nil {
		return 0, err
	}
	hash := sha256.New()
	size, hashErr := io.Copy(hash, &contextReader{ctx: ctx, input: file})
	closeErr := file.Close()
	if hashErr != nil || closeErr != nil {
		return 0, errors.Join(hashErr, closeErr)
	}
	actualDigest := fmt.Sprintf("sha256:%x", hash.Sum(nil))
	if actualDigest != expectedDigest {
		return 0, ErrBlobDigestMismatch
	}
	blobPath, err := store.blobPath(repositoryID, expectedDigest)
	if err != nil {
		return 0, err
	}
	if info, err := os.Stat(blobPath); err == nil {
		if !info.Mode().IsRegular() || info.Size() != size {
			return 0, errors.New("existing registry blob has invalid size or type")
		}
		if err := os.Remove(uploadPath); err != nil {
			return 0, err
		}
		return size, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if err := os.Rename(uploadPath, blobPath); err != nil {
		return 0, err
	}
	if err := syncDirectory(filepath.Dir(blobPath)); err != nil {
		return 0, err
	}
	return size, nil
}

func (store *PayloadStore) Cancel(repositoryID, uploadID string) error {
	path, err := store.uploadPath(repositoryID, uploadID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (store *PayloadStore) OpenBlob(repositoryID, digest string) (*os.File, int64, error) {
	path, err := store.blobPath(repositoryID, digest)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, errors.Join(err, errors.New("registry blob is not a regular file"))
	}
	return file, info.Size(), nil
}

func (store *PayloadStore) BlobExists(repositoryID, digest string) (bool, error) {
	file, _, err := store.OpenBlob(repositoryID, digest)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, file.Close()
}

func (store *PayloadStore) RepositoryBlobStats(repositoryID string) (BlobStats, error) {
	blobs, err := store.RepositoryBlobs(repositoryID)
	if err != nil {
		return BlobStats{}, err
	}
	var result BlobStats
	for _, blob := range blobs {
		if blob.Size > math.MaxInt64-result.Bytes {
			return BlobStats{}, errors.New("registry blob size overflow")
		}
		result.Count++
		result.Bytes += blob.Size
	}
	return result, nil
}

func (store *PayloadStore) RepositoryBlobs(repositoryID string) ([]BlobInfo, error) {
	if !safeComponent(repositoryID) {
		return nil, errors.New("registry repository ID is invalid")
	}
	root := filepath.Join(store.root, repositoryID, "blobs", "sha256")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]BlobInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) != 64 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("registry blob is not a regular file")
		}
		digest := "sha256:" + entry.Name()
		if err := registryname.ValidateDigest(digest); err != nil {
			return nil, errors.New("registry blob filename is not a canonical digest")
		}
		result = append(result, BlobInfo{Digest: digest, Size: info.Size(), ModifiedAt: info.ModTime()})
	}
	return result, nil
}

func (store *PayloadStore) DeleteBlob(repositoryID, digest string) error {
	path, err := store.blobPath(repositoryID, digest)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (store *PayloadStore) DeleteRepository(repositoryID string) error {
	if !safeComponent(repositoryID) {
		return errors.New("registry repository ID is invalid")
	}
	return os.RemoveAll(filepath.Join(store.root, repositoryID))
}

func (store *PayloadStore) RepositoryDirectories() ([]string, error) {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !safeComponent(entry.Name()) {
			continue
		}
		result = append(result, entry.Name())
	}
	return result, nil
}

func (store *PayloadStore) TemporaryUploads() ([]TemporaryUpload, error) {
	repositories, err := store.RepositoryDirectories()
	if err != nil {
		return nil, err
	}
	result := make([]TemporaryUpload, 0)
	for _, repositoryID := range repositories {
		root := filepath.Join(store.root, repositoryID, "uploads")
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
				continue
			}
			uploadID := strings.TrimSuffix(entry.Name(), ".part")
			if !safeComponent(uploadID) {
				return nil, errors.New("registry temporary upload filename is invalid")
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, errors.New("registry temporary upload is not a regular file")
			}
			result = append(result, TemporaryUpload{
				RepositoryID: repositoryID, UploadID: uploadID, Size: info.Size(), ModifiedAt: info.ModTime(),
			})
		}
	}
	return result, nil
}

func (store *PayloadStore) uploadRoot(repositoryID string) (string, error) {
	if !safeComponent(repositoryID) {
		return "", errors.New("registry repository ID is invalid")
	}
	root := filepath.Join(store.root, repositoryID)
	for _, directory := range []string{filepath.Join(root, "uploads"), filepath.Join(root, "blobs", "sha256")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "uploads"), nil
}

func (store *PayloadStore) uploadPath(repositoryID, uploadID string) (string, error) {
	if !safeComponent(uploadID) {
		return "", errors.New("registry upload ID is invalid")
	}
	root, err := store.uploadRoot(repositoryID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, uploadID+".part"), nil
}

func (store *PayloadStore) blobPath(repositoryID, digest string) (string, error) {
	if err := registryname.ValidateDigest(digest); err != nil {
		return "", err
	}
	if _, err := store.uploadRoot(repositoryID); err != nil {
		return "", err
	}
	return filepath.Join(store.root, repositoryID, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")), nil
}

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, "/\\\x00")
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type contextReader struct {
	ctx   context.Context
	input io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.input.Read(buffer)
}
