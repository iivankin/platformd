package diskusage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"time"
)

const DefaultResourceRefreshInterval = 5 * time.Minute

type ResourcePath struct {
	Kind       string
	ResourceID string
	ProjectID  string
	Paths      []string
}

type ResourcePathSource interface {
	ResourceDiskPaths(context.Context) ([]ResourcePath, error)
}

type ResourceUsage struct {
	Kind       string
	ResourceID string
	ProjectID  string
	Bytes      uint64
}

type ResourceSnapshot struct {
	Resources []ResourceUsage
	CheckedAt time.Time
}

// ResourceScanner measures dynamic volume paths outside the live metrics and
// HTTP request paths. A complete snapshot is published atomically so aggregate
// disk totals never mix measurements from different refreshes.
type ResourceScanner struct {
	source           ResourcePathSource
	refreshInterval  time.Duration
	entriesPerSecond int
	now              func() time.Time

	mu       sync.RWMutex
	snapshot ResourceSnapshot
}

func NewResourceScanner(source ResourcePathSource, refreshInterval time.Duration) (*ResourceScanner, error) {
	if source == nil {
		return nil, errors.New("resource disk path source is required")
	}
	if refreshInterval <= 0 {
		refreshInterval = DefaultResourceRefreshInterval
	}
	return &ResourceScanner{
		source: source, refreshInterval: refreshInterval,
		entriesPerSecond: DefaultEntriesPerSecond, now: time.Now,
	}, nil
}

func (scanner *ResourceScanner) Resources(ctx context.Context) (ResourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ResourceSnapshot{}, err
	}
	scanner.mu.RLock()
	defer scanner.mu.RUnlock()
	return ResourceSnapshot{
		Resources: append([]ResourceUsage(nil), scanner.snapshot.Resources...),
		CheckedAt: scanner.snapshot.CheckedAt,
	}, nil
}

func (scanner *ResourceScanner) Run(ctx context.Context, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}
	for {
		if err := scanner.refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			onError(err)
		}
		timer := time.NewTimer(scanner.refreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (scanner *ResourceScanner) refresh(ctx context.Context) error {
	paths, err := scanner.source.ResourceDiskPaths(ctx)
	if err != nil {
		return fmt.Errorf("list resource disk paths: %w", err)
	}
	usage := make([]ResourceUsage, 0, len(paths))
	pacer := newScanPacer(scanner.entriesPerSecond, scanner.now)
	seenResources := make(map[string]struct{}, len(paths))
	for _, resource := range paths {
		key := resource.Kind + "\x00" + resource.ResourceID
		if resource.Kind == "" || resource.ResourceID == "" || resource.ProjectID == "" {
			return errors.New("resource disk path identity is invalid")
		}
		if _, exists := seenResources[key]; exists {
			return fmt.Errorf("resource disk path identity is duplicated: %s/%s", resource.Kind, resource.ResourceID)
		}
		seenResources[key] = struct{}{}
		seenFiles := make(map[fileIdentity]struct{})
		var total uint64
		for _, path := range resource.Paths {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return fmt.Errorf("resource disk path is invalid: %q", path)
			}
			bytes, err := pathBytes(ctx, path, seenFiles, pacer)
			if err != nil {
				return fmt.Errorf("scan resource disk path %s/%s: %w", resource.Kind, resource.ResourceID, err)
			}
			if total > math.MaxUint64-bytes {
				return errors.New("resource disk usage overflows uint64")
			}
			total += bytes
		}
		usage = append(usage, ResourceUsage{
			Kind: resource.Kind, ResourceID: resource.ResourceID,
			ProjectID: resource.ProjectID, Bytes: total,
		})
	}
	scanner.mu.Lock()
	scanner.snapshot = ResourceSnapshot{Resources: usage, CheckedAt: scanner.now()}
	scanner.mu.Unlock()
	return nil
}
