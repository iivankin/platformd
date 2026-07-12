//go:build !linux || !amd64 || !cgo

package containerengine

import "context"

type StorageCleanupResult struct {
	RemovedContainers int
	PreservedImages   int
	CacheReset        bool
	ResetReason       string
}

func PrepareStorage(context.Context, Config) (StorageCleanupResult, error) {
	return StorageCleanupResult{}, ErrUnsupported
}
