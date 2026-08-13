package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type memoryStorage struct {
	mu        sync.Mutex
	buckets   map[string]map[string]memoryObject
	searches  map[string]LargestObjectsSearch
	nowMillis int64
}

type memoryObject struct {
	metadata ObjectMetadata
	body     []byte
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		buckets:   make(map[string]map[string]memoryObject),
		searches:  make(map[string]LargestObjectsSearch),
		nowMillis: time.Now().UnixMilli(),
	}
}

func (storage *memoryStorage) ReconcileBuckets(_ context.Context, storeIDs []string) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	desired := make(map[string]struct{}, len(storeIDs))
	for _, storeID := range storeIDs {
		desired[storeID] = struct{}{}
		if storage.buckets[storeID] == nil {
			storage.buckets[storeID] = make(map[string]memoryObject)
		}
	}
	for storeID := range storage.buckets {
		if _, exists := desired[storeID]; !exists {
			delete(storage.buckets, storeID)
		}
	}
	return nil
}

func (storage *memoryStorage) EnsureBucket(_ context.Context, storeID string) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.buckets[storeID] == nil {
		storage.buckets[storeID] = make(map[string]memoryObject)
	}
	return nil
}

func (storage *memoryStorage) DeleteBucket(_ context.Context, storeID string) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	delete(storage.buckets, storeID)
	return nil
}

func (storage *memoryStorage) ClearBucket(_ context.Context, storeID string) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.buckets[storeID] = make(map[string]memoryObject)
	return nil
}

func (storage *memoryStorage) Put(ctx context.Context, input PutInput) (ObjectMetadata, error) {
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return ObjectMetadata{}, err
	}
	if input.BodySizeKnown && int64(len(body)) != input.BodySize {
		return ObjectMetadata{}, ErrBadDigest
	}
	digest := sha256.Sum256(body)
	sha := hex.EncodeToString(digest[:])
	if input.ExpectedSHA256 != "" && !strings.EqualFold(input.ExpectedSHA256, sha) {
		return ObjectMetadata{}, ErrBadDigest
	}
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ObjectMetadata{}, err
	}
	objects := storage.buckets[input.StoreID]
	if objects == nil {
		return ObjectMetadata{}, state.ErrObjectStoreNotFound
	}
	current, exists := objects[input.ObjectKey]
	if err := checkMemoryPrecondition(exists, current.metadata.ETag, input.Precondition); err != nil {
		return ObjectMetadata{}, err
	}
	etag := quoteETag(sha)
	if input.PreserveETag != "" {
		etag = quoteETag(input.PreserveETag)
	}
	updated := storage.nowMillis
	if input.ModTimeMillis != 0 {
		updated = input.ModTimeMillis
	}
	metadata := ObjectMetadata{
		ObjectStoreID: input.StoreID, ObjectKey: input.ObjectKey, ContentType: input.ContentType,
		ETag: etag, Size: int64(len(body)), CreatedAtMillis: updated, UpdatedAtMillis: updated,
	}
	if exists {
		metadata.CreatedAtMillis = current.metadata.CreatedAtMillis
	}
	objects[input.ObjectKey] = memoryObject{metadata: metadata, body: append([]byte(nil), body...)}
	return metadata, nil
}

func (storage *memoryStorage) Object(_ context.Context, storeID, objectKey string) (ObjectMetadata, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	object, exists := storage.buckets[storeID][objectKey]
	if !exists {
		return ObjectMetadata{}, ErrObjectNotFound
	}
	return object.metadata, nil
}

func (storage *memoryStorage) ReadRange(_ context.Context, storeID, objectKey string, offset, length int64, output io.Writer) error {
	storage.mu.Lock()
	object, exists := storage.buckets[storeID][objectKey]
	storage.mu.Unlock()
	if !exists {
		return ErrObjectNotFound
	}
	if offset < 0 || length < 0 || offset > int64(len(object.body)) || length > int64(len(object.body))-offset {
		return errorsNew("invalid memory object range")
	}
	_, err := output.Write(object.body[offset : offset+length])
	return err
}

func (storage *memoryStorage) ListEntries(_ context.Context, storeID, prefix, delimiter, after string, limit int) ([]ObjectListEntry, bool, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	objects := storage.buckets[storeID]
	keys := make([]string, 0, len(objects))
	for key := range objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	entries := make([]ObjectListEntry, 0)
	seen := make(map[string]struct{})
	for _, key := range keys {
		entryKey := key
		if delimiter != "" {
			remainder := strings.TrimPrefix(key, prefix)
			if index := strings.Index(remainder, delimiter); index >= 0 {
				entryKey = prefix + remainder[:index+len(delimiter)]
				if entryKey <= after {
					continue
				}
				if _, exists := seen[entryKey]; !exists {
					entries = append(entries, ObjectListEntry{CommonPrefix: entryKey})
					seen[entryKey] = struct{}{}
				}
				continue
			}
		}
		if entryKey <= after {
			continue
		}
		metadata := objects[key].metadata
		entries = append(entries, ObjectListEntry{Object: &metadata})
	}
	sortObjectEntries(entries)
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	return entries, more, nil
}

func (storage *memoryStorage) Stats(_ context.Context, storeID string) (ObjectStoreStats, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	objects := storage.buckets[storeID]
	result := ObjectStoreStats{
		Ready: true, ObservedAtMillis: storage.nowMillis,
		ObjectSizeHistogram: []ObjectSizeHistogramBucket{},
	}
	for _, object := range objects {
		result.ObjectCount++
		result.TotalBytes += uint64(object.metadata.Size)
	}
	return result, nil
}

func (storage *memoryStorage) LargestObjects(_ context.Context, storeID string) (LargestObjectsSearch, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if search, exists := storage.searches[storeID]; exists {
		return search, nil
	}
	return LargestObjectsSearch{Status: LargestObjectsIdle, Objects: []LargestObject{}}, nil
}

func (storage *memoryStorage) StartLargestObjects(_ context.Context, storeID string) (LargestObjectsSearch, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	objects := make([]LargestObject, 0, len(storage.buckets[storeID]))
	for key, object := range storage.buckets[storeID] {
		objects = append(objects, LargestObject{Key: key, Size: uint64(object.metadata.Size)})
	}
	sort.Slice(objects, func(left, right int) bool {
		return objects[left].Size > objects[right].Size ||
			(objects[left].Size == objects[right].Size && objects[left].Key < objects[right].Key)
	})
	if len(objects) > 10 {
		objects = objects[:10]
	}
	search := LargestObjectsSearch{
		Status: LargestObjectsComplete, ScannedObjects: uint64(len(storage.buckets[storeID])), Objects: objects,
	}
	storage.searches[storeID] = search
	return search, nil
}

func (storage *memoryStorage) CancelLargestObjects(_ context.Context, storeID string) (LargestObjectsSearch, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	search := LargestObjectsSearch{Status: LargestObjectsCancelled, Objects: []LargestObject{}}
	storage.searches[storeID] = search
	return search, nil
}

func (storage *memoryStorage) Delete(_ context.Context, storeID, objectKey string) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	delete(storage.buckets[storeID], objectKey)
	return nil
}

func checkMemoryPrecondition(exists bool, etag string, precondition ObjectPrecondition) error {
	if precondition.IfNoneMatch && exists {
		return ErrPreconditionFailed
	}
	if precondition.IfMatch != "" && (!exists || (precondition.IfMatch != "*" && precondition.IfMatch != etag)) {
		return ErrPreconditionFailed
	}
	return nil
}

func errorsNew(value string) error { return fmt.Errorf("%s", value) }
